package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ServiceConfig contains provider-neutral feature flags and resource limits.
type ServiceConfig struct {
	Enabled            bool
	TTSEnabled         bool
	ASREnabled         bool
	MaxAnswerSeconds   int
	MaxTTSChars        int
	TTSTimeout         time.Duration
	ASRConnectTimeout  time.Duration
	ASRFinalTimeout    time.Duration
	ASRFallbackTimeout time.Duration
	TTSConcurrency     int
	ASRConcurrency     int
}

// Service owns speech providers and their process-local concurrency limits.
type Service struct {
	cfg         ServiceConfig
	synthesizer Synthesizer
	transcriber Transcriber
	ttsLimiter  *Limiter
	asrLimiter  *Limiter
}

// NewService constructs the provider-neutral speech service. Providers are
// optional so SPEECH_ENABLED=false and staged rollouts do not initialize an
// upstream connection.
func NewService(cfg ServiceConfig, synthesizer Synthesizer, transcriber Transcriber) (*Service, error) {
	if cfg.MaxAnswerSeconds <= 0 {
		return nil, fmt.Errorf("speech: max answer seconds must be positive")
	}
	if cfg.MaxTTSChars <= 0 {
		cfg.MaxTTSChars = 500
	}
	if cfg.TTSTimeout <= 0 {
		cfg.TTSTimeout = 15 * time.Second
	}
	if cfg.ASRConnectTimeout <= 0 {
		cfg.ASRConnectTimeout = 5 * time.Second
	}
	if cfg.ASRFinalTimeout <= 0 {
		cfg.ASRFinalTimeout = 8 * time.Second
	}
	if cfg.ASRFallbackTimeout <= 0 {
		cfg.ASRFallbackTimeout = 45 * time.Second
	}
	ttsLimiter, err := NewLimiter(cfg.TTSConcurrency, 1)
	if err != nil {
		return nil, fmt.Errorf("speech: create TTS limiter: %w", err)
	}
	asrLimiter, err := NewLimiter(cfg.ASRConcurrency, 1)
	if err != nil {
		return nil, fmt.Errorf("speech: create ASR limiter: %w", err)
	}
	return &Service{
		cfg:         cfg,
		synthesizer: synthesizer,
		transcriber: transcriber,
		ttsLimiter:  ttsLimiter,
		asrLimiter:  asrLimiter,
	}, nil
}

// ASRReservation owns one per-user/global limiter slot before the browser is
// upgraded to WebSocket. Closing it releases both the provider stream and the
// limiter slot exactly once.
type ASRReservation struct {
	service *Service
	release func()

	mu           sync.Mutex
	started      bool
	closed       bool
	stream       ASRStream
	streamCancel context.CancelFunc
	closeOnce    sync.Once
}

type asrStartResult struct {
	stream ASRStream
	err    error
}

// ReserveASR enforces limits before the ASR WebSocket upgrade.
func (s *Service) ReserveASR(userID string) (*ASRReservation, error) {
	if s == nil || !s.Capabilities().ASREnabled {
		return nil, ErrSpeechDisabled
	}
	release, _, ok := s.asrLimiter.AcquireDetailed(userID)
	if !ok {
		return nil, ErrLimitExceeded
	}
	return &ASRReservation{service: s, release: release}, nil
}

// MaxASRAudioBytes is the hard PCM capacity for one answer.
func (r *ASRReservation) MaxASRAudioBytes() int {
	if r == nil || r.service == nil {
		return 0
	}
	return r.service.cfg.MaxAnswerSeconds * InputSampleRate * 2
}

func (r *ASRReservation) Start(ctx context.Context, cfg ASRConfig, onEvent func(TranscriptEvent)) error {
	if r == nil || r.service == nil || r.service.transcriber == nil || onEvent == nil {
		return ErrSpeechDisabled
	}
	if cfg.QuestionID == "" || cfg.SampleRate != InputSampleRate || cfg.Channels != 1 || cfg.Format != InputFormatPCMS16LE {
		return ErrInvalidRequest
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClientCancelled
	}
	if r.started {
		r.mu.Unlock()
		return ErrInvalidRequest
	}
	r.started = true
	streamCtx, streamCancel := context.WithCancel(ctx)
	r.streamCancel = streamCancel
	r.mu.Unlock()

	resultCh := make(chan asrStartResult, 1)
	go func() {
		stream, err := r.service.transcriber.Start(streamCtx, cfg, onEvent)
		resultCh <- asrStartResult{stream: stream, err: err}
	}()

	timer := time.NewTimer(r.service.cfg.ASRConnectTimeout)
	defer timer.Stop()
	var result asrStartResult
	select {
	case result = <-resultCh:
	case <-timer.C:
		streamCancel()
		go closeLateASRStream(resultCh)
		return WithCause(ErrUpstreamTimeout, context.DeadlineExceeded)
	case <-ctx.Done():
		streamCancel()
		go closeLateASRStream(resultCh)
		return WithCause(ErrClientCancelled, ctx.Err())
	}
	if result.err != nil {
		streamCancel()
		if ctx.Err() != nil || errors.Is(result.err, ErrClientCancelled) {
			return WithCause(ErrClientCancelled, result.err)
		}
		if errors.Is(result.err, ErrUpstreamTimeout) || errors.Is(result.err, context.DeadlineExceeded) {
			return WithCause(ErrUpstreamTimeout, result.err)
		}
		if errors.Is(result.err, ErrUpstreamProtocol) {
			return WithCause(ErrUpstreamProtocol, result.err)
		}
		return WithCause(ErrUpstreamUnavailable, result.err)
	}
	if result.stream == nil {
		streamCancel()
		return WithCause(ErrUpstreamProtocol, errors.New("ASR provider returned nil stream"))
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		streamCancel()
		_ = result.stream.Close()
		return ErrClientCancelled
	}
	r.stream = result.stream
	r.mu.Unlock()
	return nil
}

func closeLateASRStream(resultCh <-chan asrStartResult) {
	result := <-resultCh
	if result.stream != nil {
		_ = result.stream.Close()
	}
}

func (r *ASRReservation) WriteAudio(ctx context.Context, pcm []byte) error {
	stream, err := r.activeStream()
	if err != nil {
		return err
	}
	if err := stream.WriteAudio(ctx, pcm); err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrClientCancelled) {
			return WithCause(ErrClientCancelled, err)
		}
		return WithCause(ErrUpstreamUnavailable, err)
	}
	return nil
}

func (r *ASRReservation) Finish(ctx context.Context) (TranscriptEvent, error) {
	stream, err := r.activeStream()
	if err != nil {
		return TranscriptEvent{}, err
	}
	finishCtx, cancel := context.WithTimeout(ctx, r.service.cfg.ASRFinalTimeout)
	defer cancel()
	result, err := stream.Finish(finishCtx)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrClientCancelled) {
			return TranscriptEvent{}, WithCause(ErrClientCancelled, err)
		}
		return TranscriptEvent{}, WithCause(ErrASRFinalFailed, err)
	}
	if result.Kind != "final" || len([]rune(result.Text)) == 0 {
		return TranscriptEvent{}, ErrEmptyAudio
	}
	return result, nil
}

// DegradeRealtime releases only the realtime provider stream. The reservation
// and its limiter slot remain owned by the browser session until HTTP fallback
// reaches a terminal result or the session is cancelled.
func (r *ASRReservation) DegradeRealtime() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClientCancelled
	}
	cancel := r.streamCancel
	stream := r.stream
	r.streamCancel = nil
	r.stream = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stream != nil {
		return stream.Close()
	}
	return nil
}

// TranscribeFallback wraps the session-owned PCM as WAV and invokes the
// provider's bounded HTTP recognizer. It never releases the ASR limiter; Close
// remains the single terminal owner of that resource.
func (r *ASRReservation) TranscribeFallback(ctx context.Context, pcm []byte) (TranscriptEvent, error) {
	if r == nil || r.service == nil || r.service.transcriber == nil {
		return TranscriptEvent{}, ErrSpeechDisabled
	}
	r.mu.Lock()
	started := r.started
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return TranscriptEvent{}, ErrClientCancelled
	}
	if !started {
		return TranscriptEvent{}, ErrInvalidRequest
	}
	wav, err := PCMToWAV(pcm, InputSampleRate, 1)
	if err != nil {
		return TranscriptEvent{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, r.service.cfg.ASRFallbackTimeout)
	defer cancel()
	result, err := r.service.transcriber.TranscribeBuffered(callCtx, wav)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrClientCancelled) {
			return TranscriptEvent{}, WithCause(ErrClientCancelled, err)
		}
		if errors.Is(err, ErrEmptyAudio) {
			return TranscriptEvent{}, ErrEmptyAudio
		}
		return TranscriptEvent{}, WithCause(ErrASRFinalFailed, err)
	}
	if result.Kind != "final" || len([]rune(result.Text)) == 0 {
		return TranscriptEvent{}, ErrEmptyAudio
	}
	result.Degraded = true
	return result, nil
}

func (r *ASRReservation) activeStream() (ASRStream, error) {
	if r == nil {
		return nil, ErrClientCancelled
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClientCancelled
	}
	if !r.started || r.stream == nil {
		return nil, ErrInvalidRequest
	}
	return r.stream, nil
}

func (r *ASRReservation) Close() error {
	if r == nil {
		return nil
	}
	var closeErr error
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		cancel := r.streamCancel
		stream := r.stream
		r.stream = nil
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if stream != nil {
			closeErr = stream.Close()
		}
		if r.release != nil {
			r.release()
		}
	})
	return closeErr
}

// PrepareText normalizes display text with the configured TTS character cap.
func (s *Service) PrepareText(text string) (string, error) {
	if s == nil {
		return "", ErrSpeechDisabled
	}
	return NormalizeText(text, s.cfg.MaxTTSChars)
}

// Capabilities reports effective, usable features rather than flags alone.
// A provider that has not been injected is never advertised to the browser.
func (s *Service) Capabilities() Capabilities {
	if s == nil {
		return DisabledCapabilities(180)
	}
	ttsEnabled := s.cfg.Enabled && s.cfg.TTSEnabled && s.synthesizer != nil
	asrEnabled := s.cfg.Enabled && s.cfg.ASREnabled && s.transcriber != nil
	capabilities := DisabledCapabilities(s.cfg.MaxAnswerSeconds)
	capabilities.Enabled = ttsEnabled || asrEnabled
	capabilities.TTSEnabled = ttsEnabled
	capabilities.ASREnabled = asrEnabled
	return capabilities
}

// Synthesize invokes the configured provider under per-user and global limits.
// Text normalization and HTTP response mapping are added in Phase 3.
func (s *Service) Synthesize(ctx context.Context, userID string, req TTSRequest) (*TTSResult, error) {
	if s == nil || !s.Capabilities().TTSEnabled {
		return nil, ErrSpeechDisabled
	}
	text, err := s.PrepareText(req.Text)
	if err != nil {
		return nil, err
	}
	req.Text = text

	release, rejection, ok := s.ttsLimiter.AcquireDetailed(userID)
	if !ok {
		switch rejection {
		case LimitPerUser:
			return nil, ErrTTSAlreadyRunning
		case LimitGlobal:
			return nil, ErrSpeechBusy
		default:
			return nil, ErrInvalidRequest
		}
	}
	releaseWithReturn := true
	defer func() {
		if releaseWithReturn {
			release()
		}
	}()

	callCtx, cancel := context.WithTimeout(ctx, s.cfg.TTSTimeout)
	defer cancel()
	result, err := s.synthesizer.Synthesize(callCtx, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, WithCause(ErrClientCancelled, err)
		}
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(err, ErrUpstreamTimeout) {
			return nil, WithCause(ErrTTSUpstreamTimeout, err)
		}
		if errors.Is(err, ErrClientCancelled) {
			return nil, err
		}
		return nil, WithCause(ErrTTSUpstreamError, err)
	}
	if result == nil || result.Audio == nil {
		return nil, WithCause(ErrTTSUpstreamError, errors.New("provider returned empty audio"))
	}
	result.Audio = &releaseReadCloser{ReadCloser: result.Audio, release: release}
	releaseWithReturn = false
	return result, nil
}

type releaseReadCloser struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (r *releaseReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.ReadCloser.Read(buffer)
	if err != nil {
		r.releaseOnce()
	}
	return n, err
}

func (r *releaseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.releaseOnce()
	return err
}

func (r *releaseReadCloser) releaseOnce() {
	r.once.Do(r.release)
}
