package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"interview-agent/internal/auth"
	"interview-agent/internal/speech"
	"interview-agent/internal/speech/dashscope"
)

const (
	asrTestOrigin     = "https://app.example"
	asrTestQuestionID = "c1267cc4-9801-4ef8-a1a0-8cae06c9432d"
)

type handlerTestASRStream struct {
	writes       chan []byte
	writeErr     error
	finishCalled chan struct{}
	finishResult chan handlerTestFinishResult
	closed       chan struct{}
	finishOnce   sync.Once
	closeOnce    sync.Once
}

type handlerTestFinishResult struct {
	event speech.TranscriptEvent
	err   error
}

func newHandlerTestASRStream() *handlerTestASRStream {
	return &handlerTestASRStream{
		writes:       make(chan []byte, 8),
		finishCalled: make(chan struct{}),
		finishResult: make(chan handlerTestFinishResult, 1),
		closed:       make(chan struct{}),
	}
}

func (s *handlerTestASRStream) WriteAudio(ctx context.Context, pcm []byte) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	copyPCM := append([]byte(nil), pcm...)
	select {
	case s.writes <- copyPCM:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *handlerTestASRStream) Finish(ctx context.Context) (speech.TranscriptEvent, error) {
	s.finishOnce.Do(func() { close(s.finishCalled) })
	select {
	case result := <-s.finishResult:
		return result.event, result.err
	case <-ctx.Done():
		return speech.TranscriptEvent{}, ctx.Err()
	}
}

func (s *handlerTestASRStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

type handlerTestTranscriber struct {
	stream        *handlerTestASRStream
	started       chan struct{}
	startErr      error
	fallbackEvent speech.TranscriptEvent
	fallbackErr   error
	fallbackCalls atomic.Int32
	fallbackFn    func(context.Context, []byte) (speech.TranscriptEvent, error)

	mu          sync.Mutex
	callback    func(speech.TranscriptEvent)
	fallbackWAV []byte
}

func newHandlerTestTranscriber(stream *handlerTestASRStream) *handlerTestTranscriber {
	return &handlerTestTranscriber{stream: stream, started: make(chan struct{})}
}

func (t *handlerTestTranscriber) Start(_ context.Context, _ speech.ASRConfig, callback func(speech.TranscriptEvent)) (speech.ASRStream, error) {
	t.mu.Lock()
	t.callback = callback
	t.mu.Unlock()
	select {
	case <-t.started:
	default:
		close(t.started)
	}
	if t.startErr != nil {
		return nil, t.startErr
	}
	return t.stream, nil
}

func (t *handlerTestTranscriber) TranscribeBuffered(ctx context.Context, wav []byte) (speech.TranscriptEvent, error) {
	t.fallbackCalls.Add(1)
	t.mu.Lock()
	t.fallbackWAV = append([]byte(nil), wav...)
	t.mu.Unlock()
	if t.fallbackFn != nil {
		return t.fallbackFn(ctx, wav)
	}
	return t.fallbackEvent, t.fallbackErr
}

func (t *handlerTestTranscriber) emit(event speech.TranscriptEvent) {
	t.mu.Lock()
	callback := t.callback
	t.mu.Unlock()
	if callback != nil {
		callback(event)
	}
}

func TestASRWebSocketStreamsPartialAndFinalInOrder(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.finishResult <- handlerTestFinishResult{event: speech.TranscriptEvent{
		Kind: "final", Text: "最终回答", ProviderRequestID: "provider-1",
	}}
	provider := newHandlerTestTranscriber(stream)
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	ready := readASRServerMessage(t, conn)
	if ready.Type != "asr.ready" || ready.QuestionID != asrTestQuestionID || ready.RequestID == "" {
		t.Fatalf("ready = %+v", ready)
	}

	pcm := make([]byte, 640)
	if err := conn.WriteMessage(websocket.BinaryMessage, pcm); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	select {
	case got := <-stream.writes:
		if len(got) != len(pcm) {
			t.Fatalf("provider PCM bytes = %d", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("provider did not receive PCM")
	}

	provider.emit(speech.TranscriptEvent{Kind: "partial", Seq: 2, Text: "较新结果"})
	partial := readASRServerMessage(t, conn)
	if partial.Type != "asr.partial" || partial.Seq != 2 || partial.Text != "较新结果" || partial.QuestionID != asrTestQuestionID {
		t.Fatalf("partial = %+v", partial)
	}
	provider.emit(speech.TranscriptEvent{Kind: "partial", Seq: 1, Text: "乱序旧结果"})
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "最终回答" || final.Degraded || final.ProviderRequestID != "provider-1" {
		t.Fatalf("final = %+v", final)
	}
	waitClosed(t, stream.closed, "provider stream")
}

func TestASRWebSocketAutoFinalizesAtPCMCapacity(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.finishResult <- handlerTestFinishResult{event: speech.TranscriptEvent{Kind: "final", Text: "达到上限", ProviderRequestID: "provider-2"}}
	provider := newHandlerTestTranscriber(stream)
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, speech.InputSampleRate*2)); err != nil {
		t.Fatalf("write capacity PCM: %v", err)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "达到上限" {
		t.Fatalf("auto final = %+v", final)
	}
	waitClosed(t, stream.closed, "provider stream")
}

func TestASRWebSocketCancelDoesNotProduceFinalAndReleasesLimit(t *testing.T) {
	firstStream := newHandlerTestASRStream()
	provider := newHandlerTestTranscriber(firstStream)
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteJSON(map[string]any{"type": "asr.cancel"}); err != nil {
		t.Fatalf("write cancel: %v", err)
	}
	waitClosed(t, firstStream.closed, "cancelled provider stream")
	select {
	case <-firstStream.finishCalled:
		t.Fatal("cancel called Finish")
	default:
	}
	if provider.fallbackCalls.Load() != 0 {
		t.Fatalf("cancel fallback calls = %d, want 0", provider.fallbackCalls.Load())
	}
	closeServer()

	secondConn, secondClose := dialASRTestServer(t, server, asrTestOrigin, "")
	_ = secondConn.Close()
	secondClose()
}

func TestASRWebSocketUpstreamDisconnectFallsBackAndReturnsFinal(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.writeErr = speech.ErrUpstreamUnavailable
	provider := newHandlerTestTranscriber(stream)
	provider.fallbackEvent = speech.TranscriptEvent{Kind: "final", Text: "HTTP 降级回答", ProviderRequestID: "fallback-write"}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	message := readASRServerMessage(t, conn)
	if message.Type != "asr.warning" || message.Code != asrDegradedCode || message.Message != asrDegradedMessage {
		t.Fatalf("disconnect warning = %+v", message)
	}
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "HTTP 降级回答" || !final.Degraded || final.ProviderRequestID != "fallback-write" {
		t.Fatalf("fallback final = %+v", final)
	}
	if provider.fallbackCalls.Load() != 1 {
		t.Fatalf("fallback calls = %d, want 1", provider.fallbackCalls.Load())
	}
	provider.mu.Lock()
	fallbackWAV := append([]byte(nil), provider.fallbackWAV...)
	provider.mu.Unlock()
	if !speech.IsWAV(fallbackWAV) || len(fallbackWAV) != 44+640 {
		t.Fatalf("fallback WAV bytes = %d", len(fallbackWAV))
	}
	waitClosed(t, stream.closed, "disconnected provider stream")
}

func TestASRWebSocketRealtimeConnectFailureFallsBackAfterRecording(t *testing.T) {
	provider := newHandlerTestTranscriber(nil)
	provider.startErr = speech.ErrUpstreamUnavailable
	provider.fallbackEvent = speech.TranscriptEvent{Kind: "final", Text: "连接失败后的回答", ProviderRequestID: "fallback-connect"}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	ready := readASRServerMessage(t, conn)
	if ready.Type != "asr.ready" {
		t.Fatalf("ready = %+v", ready)
	}
	warning := readASRServerMessage(t, conn)
	if warning.Type != "asr.warning" || warning.Code != asrDegradedCode {
		t.Fatalf("warning = %+v", warning)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "连接失败后的回答" || !final.Degraded {
		t.Fatalf("fallback final = %+v", final)
	}
}

func TestASRWebSocketRejectsStaleQuestionBeforeProviderStart(t *testing.T) {
	stream := newHandlerTestASRStream()
	provider := newHandlerTestTranscriber(stream)
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, "f1267cc4-9801-4ef8-a1a0-8cae06c9432d")
	message := readASRServerMessage(t, conn)
	if message.Type != "asr.error" || message.Code != speech.CodeInvalidRequest {
		t.Fatalf("stale question response = %+v", message)
	}
	select {
	case <-provider.started:
		t.Fatal("provider started for stale question")
	default:
	}
}

func TestASRWebSocketFinalTimeoutFallsBack(t *testing.T) {
	stream := newHandlerTestASRStream()
	provider := newHandlerTestTranscriber(stream)
	provider.fallbackEvent = speech.TranscriptEvent{Kind: "final", Text: "final 超时后的回答", ProviderRequestID: "fallback-timeout"}
	server := newASRTestServer(t, provider, 1, 20*time.Millisecond)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	message := readASRServerMessage(t, conn)
	if message.Type != "asr.warning" || message.Code != asrDegradedCode {
		t.Fatalf("timeout warning = %+v", message)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "final 超时后的回答" || !final.Degraded {
		t.Fatalf("timeout fallback final = %+v", final)
	}
	waitClosed(t, stream.closed, "timed-out provider stream")
}

func TestASRWebSocketFallbackFailureReturnsSafeError(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.writeErr = speech.ErrUpstreamUnavailable
	provider := newHandlerTestTranscriber(stream)
	provider.fallbackErr = speech.ErrUpstreamUnavailable
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	message := readASRServerMessage(t, conn)
	if message.Type != "asr.error" || message.Code != speech.CodeASRFinalFailed || !message.Retryable {
		t.Fatalf("fallback failure = %+v", message)
	}
	if strings.Contains(message.Message, "provider") {
		t.Fatalf("fallback failure leaked provider details: %+v", message)
	}
}

func TestASRWebSocketDisconnectCancelsActiveFallbackAndReleasesLimit(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.writeErr = speech.ErrUpstreamUnavailable
	provider := newHandlerTestTranscriber(stream)
	fallbackStarted := make(chan struct{})
	fallbackCancelled := make(chan struct{})
	provider.fallbackFn = func(ctx context.Context, _ []byte) (speech.TranscriptEvent, error) {
		close(fallbackStarted)
		<-ctx.Done()
		close(fallbackCancelled)
		return speech.TranscriptEvent{}, ctx.Err()
	}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	select {
	case <-fallbackStarted:
	case <-time.After(time.Second):
		t.Fatal("fallback did not start")
	}
	closeServer()
	select {
	case <-fallbackCancelled:
	case <-time.After(time.Second):
		t.Fatal("browser disconnect did not cancel fallback")
	}
	reservation := waitForASRLimitRelease(t, server.cfg.SpeechService, "default_user")
	_ = reservation.Close()
}

func TestASRWebSocketDashScopeRealtimeDisconnectUsesHTTPFallback(t *testing.T) {
	var fallbackCalls atomic.Int32
	realtimeDisconnected := make(chan struct{})
	var disconnectOnce sync.Once
	providerUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realtime":
			conn, err := providerUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.WriteJSON(map[string]any{
				"type": "session.created", "session": map[string]any{"id": "realtime-disconnect"},
			})
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": "session.updated"})
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			disconnectOnce.Do(func() { close(realtimeDisconnected) })
		case "/compatible-mode/v1/chat/completions":
			fallbackCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"fake-http-1","choices":[{"finish_reason":"stop","message":{"content":"组合适配器降级回答"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	realtimeClient, err := dashscope.NewRealtimeASRClient(dashscope.RealtimeASRConfig{
		APIKey: "fake-key", URL: websocketURLForHandler(providerServer.URL) + "/realtime",
		Model: "qwen3-asr-flash-realtime", ConnectTimeout: time.Second, WriteTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRealtimeASRClient: %v", err)
	}
	fallbackClient, err := dashscope.NewFallbackASRClient(dashscope.FallbackASRConfig{
		APIKey: "fake-key", BaseURL: providerServer.URL + "/compatible-mode/v1",
		Model: "qwen3-asr-flash", Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewFallbackASRClient: %v", err)
	}
	provider, err := dashscope.NewASRClient(realtimeClient, fallbackClient)
	if err != nil {
		t.Fatalf("NewASRClient: %v", err)
	}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	if ready := readASRServerMessage(t, conn); ready.Type != "asr.ready" {
		t.Fatalf("ready = %+v", ready)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	select {
	case <-realtimeDisconnected:
	case <-time.After(time.Second):
		t.Fatal("fake realtime provider did not disconnect")
	}
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	warning := readASRServerMessage(t, conn)
	if warning.Type != "asr.warning" || warning.Code != asrDegradedCode {
		t.Fatalf("warning = %+v", warning)
	}
	final := readASRServerMessage(t, conn)
	if final.Type != "asr.final" || final.Text != "组合适配器降级回答" || !final.Degraded || final.ProviderRequestID != "fake-http-1" {
		t.Fatalf("fallback final = %+v", final)
	}
	if fallbackCalls.Load() != 1 {
		t.Fatalf("fake HTTP fallback calls = %d, want 1", fallbackCalls.Load())
	}
}

func TestASRWebSocketRejectsOriginAndMissingTokenBeforeUpgrade(t *testing.T) {
	provider := newHandlerTestTranscriber(newHandlerTestASRStream())
	server := newASRTestServer(t, provider, 1, time.Second)

	t.Run("origin", func(t *testing.T) {
		httpServer := httptest.NewServer(server.Handler())
		defer httpServer.Close()
		header := http.Header{"Origin": []string{"https://evil.example"}}
		_, response, err := websocket.DefaultDialer.Dial(websocketURLForHandler(httpServer.URL)+"/ws/speech/asr", header)
		if err == nil {
			t.Fatal("evil origin upgraded")
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin response = %#v, err=%v", response, err)
		}
		_ = response.Body.Close()
	})

	t.Run("token", func(t *testing.T) {
		authServer := newASRTestServer(t, provider, 1, time.Second)
		authServer.cfg.AuthService = &auth.Service{}
		httpServer := httptest.NewServer(authServer.Handler())
		defer httpServer.Close()
		header := http.Header{"Origin": []string{asrTestOrigin}}
		_, response, err := websocket.DefaultDialer.Dial(websocketURLForHandler(httpServer.URL)+"/ws/speech/asr", header)
		if err == nil {
			t.Fatal("missing token upgraded")
		}
		if response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token response = %#v, err=%v", response, err)
		}
		_ = response.Body.Close()
	})
}

func newASRTestServer(t *testing.T, provider speech.Transcriber, maxSeconds int, finalTimeout time.Duration) *Server {
	t.Helper()
	return NewServer(&ServerConfig{
		SpeechService:  newASRTestService(t, provider, maxSeconds, finalTimeout),
		AllowedOrigins: []string{asrTestOrigin},
	})
}

func newASRTestService(t *testing.T, provider speech.Transcriber, maxSeconds int, finalTimeout time.Duration) *speech.Service {
	t.Helper()
	service, err := speech.NewService(speech.ServiceConfig{
		Enabled:            true,
		ASREnabled:         true,
		MaxAnswerSeconds:   maxSeconds,
		TTSConcurrency:     1,
		ASRConcurrency:     1,
		ASRConnectTimeout:  time.Second,
		ASRFinalTimeout:    finalTimeout,
		ASRFallbackTimeout: time.Second,
	}, nil, provider)
	if err != nil {
		t.Fatalf("speech.NewService: %v", err)
	}
	return service
}

func dialASRTestServer(t *testing.T, server *Server, origin, token string) (*websocket.Conn, func()) {
	t.Helper()
	httpServer := httptest.NewServer(server.Handler())
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	url := websocketURLForHandler(httpServer.URL) + "/ws/speech/asr"
	if token != "" {
		url += "?token=" + token
	}
	conn, response, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		httpServer.Close()
		t.Fatalf("dial ASR websocket: %v", err)
	}
	return conn, func() {
		_ = conn.Close()
		httpServer.Close()
	}
}

func websocketURLForHandler(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func writeASRStart(t *testing.T, conn *websocket.Conn, questionID string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"type": "asr.start", "question_id": questionID, "format": speech.InputFormatPCMS16LE,
		"sample_rate": speech.InputSampleRate, "channels": 1,
	}); err != nil {
		t.Fatalf("write asr.start: %v", err)
	}
}

func readASRServerMessage(t *testing.T, conn *websocket.Conn) asrServerMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ASR message: %v", err)
	}
	var message asrServerMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode ASR message %s: %v", data, err)
	}
	return message
}

func waitClosed(t *testing.T, closed <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s close", name)
	}
}

func waitForASRLimitRelease(t *testing.T, service *speech.Service, userID string) *speech.ASRReservation {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		reservation, err := service.ReserveASR(userID)
		if err == nil {
			return reservation
		}
		if !errors.Is(err, speech.ErrLimitExceeded) {
			t.Fatalf("ReserveASR while waiting for cleanup: %v", err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("ASR limit was not released after disconnect: %v", err)
		}
	}
}

var _ speech.ASRStream = (*handlerTestASRStream)(nil)
var _ speech.Transcriber = (*handlerTestTranscriber)(nil)
