package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSynthesizer struct {
	calls int
}

func (f *fakeSynthesizer) Synthesize(_ context.Context, _ TTSRequest) (*TTSResult, error) {
	f.calls++
	return &TTSResult{
		Audio:       io.NopCloser(strings.NewReader("RIFF")),
		ContentType: "audio/wav",
		SizeHint:    4,
	}, nil
}

type fakeTranscriber struct{}

func (*fakeTranscriber) Start(context.Context, ASRConfig, func(TranscriptEvent)) (ASRStream, error) {
	return nil, errors.New("not implemented by phase 2 fake")
}

func (*fakeTranscriber) TranscribeBuffered(context.Context, []byte) (TranscriptEvent, error) {
	return TranscriptEvent{}, errors.New("not implemented by phase 2 fake")
}

type serviceTestASRStream struct {
	closed atomic.Int32
}

func (*serviceTestASRStream) WriteAudio(context.Context, []byte) error { return nil }

func (*serviceTestASRStream) Finish(context.Context) (TranscriptEvent, error) {
	return TranscriptEvent{Kind: "final", Text: "answer"}, nil
}

func (s *serviceTestASRStream) Close() error {
	s.closed.Add(1)
	return nil
}

type serviceTestTranscriber struct {
	stream         *serviceTestASRStream
	err            error
	fallbackResult TranscriptEvent
	fallbackErr    error
	fallbackWAV    []byte
	fallbackFn     func(context.Context, []byte) (TranscriptEvent, error)
}

func (t *serviceTestTranscriber) Start(context.Context, ASRConfig, func(TranscriptEvent)) (ASRStream, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.stream, nil
}

func (t *serviceTestTranscriber) TranscribeBuffered(ctx context.Context, wav []byte) (TranscriptEvent, error) {
	t.fallbackWAV = append([]byte(nil), wav...)
	if t.fallbackFn != nil {
		return t.fallbackFn(ctx, wav)
	}
	return t.fallbackResult, t.fallbackErr
}

type serviceBlockingTranscriber struct {
	done chan struct{}
}

type isolatedASRTranscriber struct{}

func (*isolatedASRTranscriber) Start(_ context.Context, cfg ASRConfig, _ func(TranscriptEvent)) (ASRStream, error) {
	return &isolatedASRStream{questionID: cfg.QuestionID}, nil
}

func (*isolatedASRTranscriber) TranscribeBuffered(context.Context, []byte) (TranscriptEvent, error) {
	return TranscriptEvent{}, ErrASRFinalFailed
}

type isolatedASRStream struct {
	mu         sync.Mutex
	questionID string
	marker     byte
}

func (s *isolatedASRStream) WriteAudio(_ context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	s.marker = pcm[0]
	s.mu.Unlock()
	return nil
}

func (s *isolatedASRStream) Finish(context.Context) (TranscriptEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TranscriptEvent{Kind: "final", Text: fmt.Sprintf("%s:%d", s.questionID, s.marker)}, nil
}

func (*isolatedASRStream) Close() error { return nil }

func (t *serviceBlockingTranscriber) Start(ctx context.Context, _ ASRConfig, _ func(TranscriptEvent)) (ASRStream, error) {
	<-ctx.Done()
	close(t.done)
	return nil, ctx.Err()
}

func (*serviceBlockingTranscriber) TranscribeBuffered(context.Context, []byte) (TranscriptEvent, error) {
	return TranscriptEvent{}, ErrASRFinalFailed
}

func TestServiceCapabilitiesRequireEnabledProvider(t *testing.T) {
	cfg := ServiceConfig{
		Enabled:          true,
		TTSEnabled:       true,
		ASREnabled:       true,
		MaxAnswerSeconds: 180,
		TTSConcurrency:   20,
		ASRConcurrency:   20,
	}

	withoutProviders, err := NewService(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewService without providers: %v", err)
	}
	if got := withoutProviders.Capabilities(); got.Enabled || got.TTSEnabled || got.ASREnabled {
		t.Fatalf("capabilities without providers = %+v, want all disabled", got)
	}

	withProviders, err := NewService(cfg, &fakeSynthesizer{}, &fakeTranscriber{})
	if err != nil {
		t.Fatalf("NewService with providers: %v", err)
	}
	got := withProviders.Capabilities()
	if !got.Enabled || !got.TTSEnabled || !got.ASREnabled {
		t.Fatalf("capabilities with providers = %+v, want all enabled", got)
	}
	if got.MaxAnswerSeconds != 180 || got.InputFormat != InputFormatPCMS16LE || got.InputSampleRate != InputSampleRate {
		t.Fatalf("unexpected capabilities defaults: %+v", got)
	}
}

func TestServiceSynthesizeUsesProviderAndReleasesLimit(t *testing.T) {
	provider := &fakeSynthesizer{}
	service, err := NewService(ServiceConfig{
		Enabled:          true,
		TTSEnabled:       true,
		MaxAnswerSeconds: 180,
		TTSConcurrency:   1,
		ASRConcurrency:   1,
	}, provider, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for i := 0; i < 2; i++ {
		result, err := service.Synthesize(context.Background(), "alice", TTSRequest{Text: "hello"})
		if err != nil {
			t.Fatalf("Synthesize call %d: %v", i+1, err)
		}
		_ = result.Audio.Close()
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
}

func TestDisabledServiceDoesNotCallProvider(t *testing.T) {
	provider := &fakeSynthesizer{}
	service, err := NewService(ServiceConfig{
		Enabled:          false,
		TTSEnabled:       true,
		MaxAnswerSeconds: 180,
		TTSConcurrency:   1,
		ASRConcurrency:   1,
	}, provider, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = service.Synthesize(context.Background(), "alice", TTSRequest{Text: "hello"})
	if !errors.Is(err, ErrSpeechDisabled) {
		t.Fatalf("Synthesize error = %v, want ErrSpeechDisabled", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestServiceASRReservationOwnsLimitAndStream(t *testing.T) {
	providerStream := &serviceTestASRStream{}
	service, err := NewService(ServiceConfig{
		Enabled:           true,
		ASREnabled:        true,
		MaxAnswerSeconds:  1,
		TTSConcurrency:    1,
		ASRConcurrency:    1,
		ASRConnectTimeout: time.Second,
		ASRFinalTimeout:   time.Second,
	}, nil, &serviceTestTranscriber{stream: providerStream})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	reservation, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR: %v", err)
	}
	if _, err := service.ReserveASR("alice"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("second ReserveASR error = %v, want ErrLimitExceeded", err)
	}
	if err := reservation.Start(context.Background(), ASRConfig{
		QuestionID: "question-1",
		SampleRate: InputSampleRate,
		Channels:   1,
		Format:     InputFormatPCMS16LE,
	}, func(TranscriptEvent) {}); err != nil {
		t.Fatalf("reservation.Start: %v", err)
	}
	if err := reservation.WriteAudio(context.Background(), []byte{1, 2}); err != nil {
		t.Fatalf("reservation.WriteAudio: %v", err)
	}
	result, err := reservation.Finish(context.Background())
	if err != nil || result.Text != "answer" {
		t.Fatalf("reservation.Finish = %+v, %v", result, err)
	}
	if err := reservation.Close(); err != nil {
		t.Fatalf("reservation.Close: %v", err)
	}
	if err := reservation.Close(); err != nil {
		t.Fatalf("second reservation.Close: %v", err)
	}
	if providerStream.closed.Load() != 1 {
		t.Fatalf("provider close calls = %d, want 1", providerStream.closed.Load())
	}
	second, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR after close: %v", err)
	}
	_ = second.Close()
}

func TestServiceASRReservationValidatesConfig(t *testing.T) {
	service, err := NewService(ServiceConfig{
		Enabled:          true,
		ASREnabled:       true,
		MaxAnswerSeconds: 1,
		TTSConcurrency:   1,
		ASRConcurrency:   1,
	}, nil, &serviceTestTranscriber{stream: &serviceTestASRStream{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reservation, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR: %v", err)
	}
	defer reservation.Close()
	if err := reservation.Start(context.Background(), ASRConfig{
		QuestionID: "question-1", SampleRate: 8000, Channels: 2, Format: "wav",
	}, func(TranscriptEvent) {}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Start error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceASRReservationPreservesProviderProtocolError(t *testing.T) {
	service, err := NewService(ServiceConfig{
		Enabled:          true,
		ASREnabled:       true,
		MaxAnswerSeconds: 1,
		TTSConcurrency:   1,
		ASRConcurrency:   1,
	}, nil, &serviceTestTranscriber{err: ErrUpstreamProtocol})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reservation, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR: %v", err)
	}
	defer reservation.Close()
	err = reservation.Start(context.Background(), ASRConfig{
		QuestionID: "question-1", SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
	}, func(TranscriptEvent) {})
	if !errors.Is(err, ErrUpstreamProtocol) {
		t.Fatalf("Start error = %v, want ErrUpstreamProtocol", err)
	}
}

func TestServiceASRConnectTimeoutStopsProviderAndReleasesLimit(t *testing.T) {
	provider := &serviceBlockingTranscriber{done: make(chan struct{})}
	service, err := NewService(ServiceConfig{
		Enabled:           true,
		ASREnabled:        true,
		MaxAnswerSeconds:  1,
		TTSConcurrency:    1,
		ASRConcurrency:    1,
		ASRConnectTimeout: 20 * time.Millisecond,
	}, nil, provider)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reservation, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR: %v", err)
	}
	err = reservation.Start(context.Background(), ASRConfig{
		QuestionID: "question-1", SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
	}, func(TranscriptEvent) {})
	if !errors.Is(err, ErrUpstreamTimeout) {
		t.Fatalf("Start error = %v, want ErrUpstreamTimeout", err)
	}
	select {
	case <-provider.done:
	case <-time.After(time.Second):
		t.Fatal("provider Start goroutine did not stop after timeout")
	}
	if err := reservation.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	second, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR after timed-out request: %v", err)
	}
	_ = second.Close()
}

func TestServiceASRFallbackWrapsPCMAndKeepsReservation(t *testing.T) {
	providerStream := &serviceTestASRStream{}
	provider := &serviceTestTranscriber{
		stream:         providerStream,
		fallbackResult: TranscriptEvent{Kind: "final", Text: "降级回答", ProviderRequestID: "fallback-1"},
	}
	service, err := NewService(ServiceConfig{
		Enabled: true, ASREnabled: true, MaxAnswerSeconds: 1,
		TTSConcurrency: 1, ASRConcurrency: 1, ASRFallbackTimeout: time.Second,
	}, nil, provider)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	reservation, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR: %v", err)
	}
	if err := reservation.Start(context.Background(), ASRConfig{
		QuestionID: "question-1", SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
	}, func(TranscriptEvent) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := reservation.DegradeRealtime(); err != nil {
		t.Fatalf("DegradeRealtime: %v", err)
	}
	if providerStream.closed.Load() != 1 {
		t.Fatalf("realtime close calls = %d, want 1", providerStream.closed.Load())
	}
	if _, err := service.ReserveASR("alice"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("reservation released before fallback terminal state: %v", err)
	}

	pcm := []byte{1, 2, 3, 4, 5, 6}
	result, err := reservation.TranscribeFallback(context.Background(), pcm)
	if err != nil {
		t.Fatalf("TranscribeFallback: %v", err)
	}
	if result.Text != "降级回答" || !result.Degraded || result.ProviderRequestID != "fallback-1" {
		t.Fatalf("fallback result = %+v", result)
	}
	if !IsWAV(provider.fallbackWAV) || len(provider.fallbackWAV) != 44+len(pcm) || string(provider.fallbackWAV[44:]) != string(pcm) {
		t.Fatalf("fallback WAV invalid: bytes=%d", len(provider.fallbackWAV))
	}
	if err := reservation.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	second, err := service.ReserveASR("alice")
	if err != nil {
		t.Fatalf("ReserveASR after fallback close: %v", err)
	}
	_ = second.Close()
}

func TestServiceASRFallbackTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		providerDone := make(chan struct{})
		provider := &serviceTestTranscriber{stream: &serviceTestASRStream{}}
		provider.fallbackFn = func(ctx context.Context, _ []byte) (TranscriptEvent, error) {
			<-ctx.Done()
			close(providerDone)
			return TranscriptEvent{}, ctx.Err()
		}
		service, err := NewService(ServiceConfig{
			Enabled: true, ASREnabled: true, MaxAnswerSeconds: 1,
			TTSConcurrency: 1, ASRConcurrency: 1, ASRFallbackTimeout: 20 * time.Millisecond,
		}, nil, provider)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		reservation, err := service.ReserveASR("alice")
		if err != nil {
			t.Fatalf("ReserveASR: %v", err)
		}
		defer reservation.Close()
		if err := reservation.Start(context.Background(), ASRConfig{
			QuestionID: "question-1", SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
		}, func(TranscriptEvent) {}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		_, err = reservation.TranscribeFallback(context.Background(), make([]byte, 640))
		if !errors.Is(err, ErrASRFinalFailed) {
			t.Fatalf("fallback timeout error = %v, want ErrASRFinalFailed", err)
		}
		select {
		case <-providerDone:
		case <-time.After(time.Second):
			t.Fatal("fallback provider did not stop after timeout")
		}
	})

	t.Run("cancel", func(t *testing.T) {
		provider := &serviceTestTranscriber{stream: &serviceTestASRStream{}}
		provider.fallbackFn = func(ctx context.Context, _ []byte) (TranscriptEvent, error) {
			<-ctx.Done()
			return TranscriptEvent{}, ctx.Err()
		}
		service, err := NewService(ServiceConfig{
			Enabled: true, ASREnabled: true, MaxAnswerSeconds: 1,
			TTSConcurrency: 1, ASRConcurrency: 1, ASRFallbackTimeout: time.Second,
		}, nil, provider)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		reservation, err := service.ReserveASR("alice")
		if err != nil {
			t.Fatalf("ReserveASR: %v", err)
		}
		defer reservation.Close()
		if err := reservation.Start(context.Background(), ASRConfig{
			QuestionID: "question-1", SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
		}, func(TranscriptEvent) {}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = reservation.TranscribeFallback(ctx, make([]byte, 640))
		if !errors.Is(err, ErrClientCancelled) {
			t.Fatalf("fallback cancellation error = %v, want ErrClientCancelled", err)
		}
	})
}

func TestServiceASRTwentyConcurrentSessionsRemainIsolated(t *testing.T) {
	const sessionCount = 20
	service, err := NewService(ServiceConfig{
		Enabled: true, ASREnabled: true, MaxAnswerSeconds: 1,
		TTSConcurrency: 1, ASRConcurrency: sessionCount,
		ASRConnectTimeout: time.Second, ASRFinalTimeout: time.Second,
	}, nil, &isolatedASRTranscriber{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ready := make(chan struct{}, sessionCount)
	start := make(chan struct{})
	results := make(chan error, sessionCount)
	for index := 0; index < sessionCount; index++ {
		go func(index int) {
			userID := fmt.Sprintf("user-%02d", index)
			questionID := fmt.Sprintf("question-%02d", index)
			reservation, reserveErr := service.ReserveASR(userID)
			ready <- struct{}{}
			if reserveErr != nil {
				results <- fmt.Errorf("reserve %s: %w", userID, reserveErr)
				return
			}
			defer reservation.Close()
			<-start
			if startErr := reservation.Start(context.Background(), ASRConfig{
				QuestionID: questionID, SampleRate: InputSampleRate, Channels: 1, Format: InputFormatPCMS16LE,
			}, func(TranscriptEvent) {}); startErr != nil {
				results <- fmt.Errorf("start %s: %w", userID, startErr)
				return
			}
			pcm := make([]byte, 640)
			pcm[0] = byte(index)
			if writeErr := reservation.WriteAudio(context.Background(), pcm); writeErr != nil {
				results <- fmt.Errorf("write %s: %w", userID, writeErr)
				return
			}
			result, finishErr := reservation.Finish(context.Background())
			if finishErr != nil {
				results <- fmt.Errorf("finish %s: %w", userID, finishErr)
				return
			}
			want := fmt.Sprintf("%s:%d", questionID, index)
			if result.Text != want {
				results <- fmt.Errorf("session %s got %q, want %q", userID, result.Text, want)
				return
			}
			results <- nil
		}(index)
	}
	for index := 0; index < sessionCount; index++ {
		<-ready
	}
	close(start)
	for index := 0; index < sessionCount; index++ {
		if resultErr := <-results; resultErr != nil {
			t.Error(resultErr)
		}
	}
	if t.Failed() {
		return
	}
	reservation, err := service.ReserveASR("after-concurrency")
	if err != nil {
		t.Fatalf("ASR limiter was not fully released: %v", err)
	}
	_ = reservation.Close()
}
