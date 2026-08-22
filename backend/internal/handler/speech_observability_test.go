package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"interview-agent/internal/speech"
)

type recordingSpeechEventLogger struct {
	mu     sync.Mutex
	events []speech.Event
}

func (l *recordingSpeechEventLogger) LogSpeechEvent(_ context.Context, event speech.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *recordingSpeechEventLogger) snapshot() []speech.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]speech.Event(nil), l.events...)
}

func (l *recordingSpeechEventLogger) waitForCount(t *testing.T, count int) []speech.Event {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		events := l.snapshot()
		if len(events) >= count {
			return events
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("speech events = %d, want at least %d: %+v", len(events), count, events)
		}
	}
}

func TestTTSHandlerEmitsStructuredLifecycleEvents(t *testing.T) {
	logger := &recordingSpeechEventLogger{}
	server := NewServer(&ServerConfig{
		SpeechService: newFakeSpeechService(t, true), SpeechLogger: logger,
		SpeechProvider: "fake", SpeechTTSModel: "fake-tts",
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/speech/tts", strings.NewReader(`{"question_id":"q1","text":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "observability-1")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	events := logger.snapshot()
	if len(events) != 2 || events[0].Name != speech.EventTTSStarted || events[1].Name != speech.EventTTSCompleted {
		t.Fatalf("TTS events = %+v", events)
	}
	if events[1].RequestID != "observability-1" || events[1].AudioBytes != 4 || events[1].TextChars != 5 || events[1].Provider != "fake" {
		t.Fatalf("TTS completed event = %+v", events[1])
	}
}

func TestASRWebSocketEmitsRealtimeAndFallbackEvents(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.writeErr = speech.ErrUpstreamUnavailable
	provider := newHandlerTestTranscriber(stream)
	provider.fallbackEvent = speech.TranscriptEvent{Kind: "final", Text: "fallback answer", ProviderRequestID: "fallback-event"}
	logger := &recordingSpeechEventLogger{}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.cfg.SpeechLogger = logger
	server.cfg.SpeechProvider = "fake"
	server.cfg.SpeechASRRealtimeModel = "fake-realtime"
	server.cfg.SpeechASRFallbackModel = "fake-fallback"
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
	_ = readASRServerMessage(t, conn)

	events := logger.waitForCount(t, 4)
	wantNames := []string{
		speech.EventASRStarted,
		speech.EventASRRealtimeDegraded,
		speech.EventASRFallbackStarted,
		speech.EventASRCompleted,
	}
	if len(events) != len(wantNames) {
		t.Fatalf("ASR events = %+v", events)
	}
	for index, name := range wantNames {
		if events[index].Name != name {
			t.Fatalf("ASR event %d = %+v, want %s", index, events[index], name)
		}
	}
	completed := events[len(events)-1]
	if !completed.Degraded || completed.AudioBytes != 640 || completed.TextChars != len("fallback answer") || completed.Model != "fake-fallback" {
		t.Fatalf("ASR completed event = %+v", completed)
	}
}

func TestASRWebSocketEmitsFirstPartialLatencyOnce(t *testing.T) {
	stream := newHandlerTestASRStream()
	stream.finishResult <- handlerTestFinishResult{event: speech.TranscriptEvent{Kind: "final", Text: "answer"}}
	provider := newHandlerTestTranscriber(stream)
	logger := &recordingSpeechEventLogger{}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.cfg.SpeechLogger = logger
	server.speechQuestions.Set("default_user", asrTestQuestionID)

	conn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	writeASRStart(t, conn, asrTestQuestionID)
	_ = readASRServerMessage(t, conn)
	provider.emit(speech.TranscriptEvent{Kind: "partial", Seq: 1, Text: "first"})
	_ = readASRServerMessage(t, conn)
	provider.emit(speech.TranscriptEvent{Kind: "partial", Seq: 2, Text: "second"})
	_ = readASRServerMessage(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "asr.stop"}); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	_ = readASRServerMessage(t, conn)

	partialEvents := 0
	for _, event := range logger.waitForCount(t, 3) {
		if event.Name == speech.EventASRFirstPartial {
			partialEvents++
			if event.TextChars != 5 || event.FirstResultMS < 0 {
				t.Fatalf("first partial event = %+v", event)
			}
		}
	}
	if partialEvents != 1 {
		t.Fatalf("first partial events = %d, want 1", partialEvents)
	}
}

func TestASRLimitRejectionEmitsStructuredEvent(t *testing.T) {
	provider := newHandlerTestTranscriber(newHandlerTestASRStream())
	logger := &recordingSpeechEventLogger{}
	server := newASRTestServer(t, provider, 1, time.Second)
	server.cfg.SpeechLogger = logger
	server.cfg.SpeechProvider = "fake"
	server.cfg.SpeechASRRealtimeModel = "fake-realtime"

	firstConn, closeServer := dialASRTestServer(t, server, asrTestOrigin, "")
	defer closeServer()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	header := http.Header{"Origin": []string{asrTestOrigin}}
	_, response, err := websocket.DefaultDialer.Dial(websocketURLForHandler(httpServer.URL)+"/ws/speech/asr", header)
	if err == nil {
		t.Fatal("second ASR connection bypassed per-user limit")
	}
	if response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("limit response = %#v, err=%v", response, err)
	}
	_ = response.Body.Close()
	_ = firstConn.Close()

	events := logger.snapshot()
	if len(events) != 1 || events[0].Name != speech.EventLimitRejected || events[0].ErrorCode != speech.CodeLimitExceeded {
		t.Fatalf("limit events = %+v", events)
	}
}

var _ speech.EventLogger = (*recordingSpeechEventLogger)(nil)
