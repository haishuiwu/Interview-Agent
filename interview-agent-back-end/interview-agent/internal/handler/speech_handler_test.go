package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"interview-agent/internal/auth"
	"interview-agent/internal/speech"
)

type fakeSpeechSynthesizer struct {
	synthesize func(context.Context, speech.TTSRequest) (*speech.TTSResult, error)
}

func (f *fakeSpeechSynthesizer) Synthesize(ctx context.Context, req speech.TTSRequest) (*speech.TTSResult, error) {
	if f.synthesize != nil {
		return f.synthesize(ctx, req)
	}
	return &speech.TTSResult{
		Audio:       io.NopCloser(strings.NewReader("RIFF")),
		ContentType: "audio/wav",
		Provider:    "fake",
		SizeHint:    4,
	}, nil
}

type fakeSpeechTranscriber struct{}

func (*fakeSpeechTranscriber) Start(context.Context, speech.ASRConfig, func(speech.TranscriptEvent)) (speech.ASRStream, error) {
	return nil, errors.New("not implemented by phase 2 fake")
}

func (*fakeSpeechTranscriber) TranscribeBuffered(context.Context, []byte) (speech.TranscriptEvent, error) {
	return speech.TranscriptEvent{}, errors.New("not implemented by phase 2 fake")
}

func newFakeSpeechService(t *testing.T, enabled bool) *speech.Service {
	t.Helper()
	return newTTSTestService(t, enabled, &fakeSpeechSynthesizer{}, 20, time.Second, 500)
}

func newTTSTestService(
	t *testing.T,
	enabled bool,
	synthesizer speech.Synthesizer,
	concurrency int,
	timeout time.Duration,
	maxChars int,
) *speech.Service {
	t.Helper()
	service, err := speech.NewService(speech.ServiceConfig{
		Enabled:          enabled,
		TTSEnabled:       true,
		ASREnabled:       true,
		MaxAnswerSeconds: 180,
		MaxTTSChars:      maxChars,
		TTSTimeout:       timeout,
		TTSConcurrency:   concurrency,
		ASRConcurrency:   20,
	}, synthesizer, &fakeSpeechTranscriber{})
	if err != nil {
		t.Fatalf("speech.NewService: %v", err)
	}
	return service
}

func TestTTSHandlerSuccess(t *testing.T) {
	var providerRequest speech.TTSRequest
	synthesizer := &fakeSpeechSynthesizer{synthesize: func(_ context.Context, req speech.TTSRequest) (*speech.TTSResult, error) {
		providerRequest = req
		return &speech.TTSResult{
			Audio:             io.NopCloser(strings.NewReader("RIFFaudio")),
			ContentType:       "audio/wav",
			Provider:          "fake",
			ProviderRequestID: "provider-request",
			SizeHint:          9,
		}, nil
	}}
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, true, synthesizer, 20, time.Second, 500)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{
		"question_id":"question-1",
		"text":"**请介绍** Go。"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "client-request")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != "RIFFaudio" {
		t.Fatalf("audio body = %q", got)
	}
	if recorder.Header().Get("X-Request-ID") != "client-request" || recorder.Header().Get("X-Speech-Provider") != "fake" {
		t.Fatalf("unexpected response headers: %v", recorder.Header())
	}
	if providerRequest.RequestID != "client-request" || providerRequest.QuestionID != "question-1" || providerRequest.Text != "请介绍 Go。" {
		t.Fatalf("provider request = %+v", providerRequest)
	}
}

func TestTTSHandlerRejectsLongText(t *testing.T) {
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, true, &fakeSpeechSynthesizer{}, 20, time.Second, 5)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"`+strings.Repeat("好", 6)+`"}`))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	assertSpeechError(t, recorder, http.StatusRequestEntityTooLarge, speech.CodeTextTooLong)
}

func TestTTSHandlerRejectsOversizedJSONBody(t *testing.T) {
	server := NewServer(&ServerConfig{SpeechService: newFakeSpeechService(t, true)})
	recorder := httptest.NewRecorder()
	body := `{"question_id":"q1","text":"hello"}` + strings.Repeat(" ", maxTTSRequestBytes)
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	assertSpeechError(t, recorder, http.StatusRequestEntityTooLarge, speech.CodeInvalidRequest)
}

func TestTTSHandlerDisabled(t *testing.T) {
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, false, &fakeSpeechSynthesizer{}, 20, time.Second, 500)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"hello"}`))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	assertSpeechError(t, recorder, http.StatusServiceUnavailable, speech.CodeSpeechDisabled)
}

func TestTTSHandlerMapsUpstreamError(t *testing.T) {
	synthesizer := &fakeSpeechSynthesizer{synthesize: func(context.Context, speech.TTSRequest) (*speech.TTSResult, error) {
		return nil, speech.ErrUpstreamUnavailable
	}}
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, true, synthesizer, 20, time.Second, 500)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"hello"}`))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	assertSpeechError(t, recorder, http.StatusBadGateway, speech.CodeTTSUpstreamError)
}

func TestTTSHandlerTimesOut(t *testing.T) {
	synthesizer := &fakeSpeechSynthesizer{synthesize: func(ctx context.Context, _ speech.TTSRequest) (*speech.TTSResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, true, synthesizer, 20, 20*time.Millisecond, 500)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"hello"}`))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	assertSpeechError(t, recorder, http.StatusGatewayTimeout, speech.CodeTTSUpstreamTimeout)
}

func TestTTSHandlerRejectsConcurrentRequestForUser(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	synthesizer := &fakeSpeechSynthesizer{synthesize: func(ctx context.Context, _ speech.TTSRequest) (*speech.TTSResult, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			return &speech.TTSResult{Audio: io.NopCloser(strings.NewReader("RIFF")), ContentType: "audio/wav", Provider: "fake"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	server := NewServer(&ServerConfig{SpeechService: newTTSTestService(t, true, synthesizer, 1, time.Second, 500)})
	handler := server.Handler()

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"first"}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		firstDone <- recorder
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first TTS request did not reach provider")
	}

	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q2","text":"second"}`))
	secondRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(secondRecorder, secondRequest)
	assertSpeechError(t, secondRecorder, http.StatusConflict, speech.CodeTTSAlreadyRunning)

	close(release)
	select {
	case firstRecorder := <-firstDone:
		if firstRecorder.Code != http.StatusOK {
			t.Fatalf("first status = %d; body=%s", firstRecorder.Code, firstRecorder.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("first TTS request did not complete")
	}
}

func assertSpeechError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var response speechErrorBody
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", response.Error.Code, wantCode)
	}
	if response.RequestID == "" {
		t.Fatal("error response is missing request_id")
	}
}

func TestSpeechCapabilitiesWithFakeProviders(t *testing.T) {
	server := NewServer(&ServerConfig{SpeechService: newFakeSpeechService(t, true)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/speech/capabilities", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var got speech.Capabilities
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Enabled || !got.TTSEnabled || !got.ASREnabled {
		t.Fatalf("capabilities = %+v, want fake providers enabled", got)
	}
	if got.InputFormat != speech.InputFormatPCMS16LE || got.InputSampleRate != speech.InputSampleRate {
		t.Fatalf("unexpected input capabilities: %+v", got)
	}
	if strings.Contains(recorder.Body.String(), "key") || strings.Contains(recorder.Body.String(), "dashscope.aliyuncs.com") {
		t.Fatalf("capabilities leaked provider configuration: %s", recorder.Body.String())
	}
}

func TestSpeechCapabilitiesDisabledWithoutService(t *testing.T) {
	server := NewServer(&ServerConfig{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/speech/capabilities", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var got speech.Capabilities
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Enabled || got.TTSEnabled || got.ASREnabled {
		t.Fatalf("capabilities = %+v, want disabled", got)
	}
}

func TestSpeechCapabilitiesDisabledByFeatureFlag(t *testing.T) {
	server := NewServer(&ServerConfig{SpeechService: newFakeSpeechService(t, false)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/speech/capabilities", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var got speech.Capabilities
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Enabled || got.TTSEnabled || got.ASREnabled {
		t.Fatalf("capabilities = %+v, want feature flag to disable providers", got)
	}
}

func TestSpeechCapabilitiesRequiresBearerToken(t *testing.T) {
	server := NewServer(&ServerConfig{
		AuthService:   &auth.Service{},
		SpeechService: newFakeSpeechService(t, true),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/speech/capabilities", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(recorder.Body.String(), speech.CodeUnauthorized) {
		t.Fatalf("body = %s, want %s", recorder.Body.String(), speech.CodeUnauthorized)
	}
}

func TestSpeechCapabilitiesAcceptsValidBearerToken(t *testing.T) {
	authService := &auth.Service{}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "test-user",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	signedToken, err := token.SignedString([]byte{})
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	server := NewServer(&ServerConfig{
		AuthService:   authService,
		SpeechService: newFakeSpeechService(t, true),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/speech/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestSpeechCapabilitiesCORS(t *testing.T) {
	server := NewServer(&ServerConfig{
		SpeechService:  newFakeSpeechService(t, true),
		AllowedOrigins: []string{"https://app.example"},
	})

	t.Run("allowed origin", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodOptions, "/api/speech/capabilities", nil)
		request.Header.Set("Origin", "https://app.example")
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Fatalf("allow origin = %q", got)
		}
		if !strings.Contains(recorder.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Fatal("Authorization header is not allowed by CORS")
		}
	})

	t.Run("rejected origin", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodOptions, "/api/speech/capabilities", nil)
		request.Header.Set("Origin", "https://evil.example")
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
		}
	})
}
