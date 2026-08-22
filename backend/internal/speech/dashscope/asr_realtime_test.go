package dashscope

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"interview-agent/internal/speech"
)

func TestRealtimeASRManualProtocol(t *testing.T) {
	serverDone := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		if r.URL.Query().Get("model") != "qwen3-asr-flash-realtime" {
			t.Errorf("model query = %q", r.URL.Query().Get("model"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		mustWriteProviderJSON(t, conn, map[string]any{
			"type":    "session.created",
			"session": map[string]any{"id": "sess-1", "model": "qwen3-asr-flash-realtime"},
		})

		update := mustReadProviderJSON(t, conn)
		if update["type"] != "session.update" {
			t.Errorf("first client event = %v", update["type"])
		}
		session, ok := update["session"].(map[string]any)
		if !ok {
			t.Errorf("session.update session = %#v", update["session"])
			return
		}
		if session["input_audio_format"] != "pcm" || session["sample_rate"] != float64(16000) || session["turn_detection"] != nil {
			t.Errorf("session.update = %#v", session)
		}
		mustWriteProviderJSON(t, conn, map[string]any{
			"type":    "session.updated",
			"session": map[string]any{"id": "sess-1"},
		})

		appendEvent := mustReadProviderJSON(t, conn)
		if appendEvent["type"] != "input_audio_buffer.append" {
			t.Errorf("audio event = %v", appendEvent["type"])
		}
		audio, err := base64.StdEncoding.DecodeString(appendEvent["audio"].(string))
		if err != nil || string(audio) != "\x01\x02\x03\x04" {
			t.Errorf("audio = %v, %v", audio, err)
		}
		mustWriteProviderJSON(t, conn, map[string]any{
			"type":  "conversation.item.input_audio_transcription.text",
			"text":  "我认为",
			"stash": "可以",
		})

		commit := mustReadProviderJSON(t, conn)
		finish := mustReadProviderJSON(t, conn)
		if commit["type"] != "input_audio_buffer.commit" || finish["type"] != "session.finish" {
			t.Errorf("finish events = %v, %v", commit["type"], finish["type"])
		}
		mustWriteProviderJSON(t, conn, map[string]any{
			"type":       "conversation.item.input_audio_transcription.completed",
			"transcript": "我认为可以",
		})
		mustWriteProviderJSON(t, conn, map[string]any{"type": "session.finished"})
	}))
	defer server.Close()

	client := newRealtimeASRTestClient(t, websocketURL(server.URL), time.Second)
	events := make(chan speech.TranscriptEvent, 2)
	stream, err := client.Start(context.Background(), speech.ASRConfig{
		QuestionID: "question-1", SampleRate: speech.InputSampleRate, Channels: 1, Format: speech.InputFormatPCMS16LE,
	}, func(event speech.TranscriptEvent) { events <- event })
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer stream.Close()
	if err := stream.WriteAudio(context.Background(), []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("WriteAudio: %v", err)
	}
	select {
	case event := <-events:
		if event.Kind != "partial" || event.Seq != 1 || event.Text != "我认为可以" || event.ProviderRequestID != "sess-1" {
			t.Fatalf("partial event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for partial event")
	}
	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if result.Kind != "final" || result.Text != "我认为可以" || result.ProviderRequestID != "sess-1" {
		t.Fatalf("final event = %+v", result)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("provider server did not finish")
	}
}

func TestRealtimeASRStartMapsProviderError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mustWriteProviderJSON(t, conn, map[string]any{
			"type": "session.created", "session": map[string]any{"id": "sess-error"},
		})
		_ = mustReadProviderJSON(t, conn)
		mustWriteProviderJSON(t, conn, map[string]any{
			"type": "error", "error": map[string]any{"code": "invalid_value", "message": "secret upstream detail"},
		})
	}))
	defer server.Close()

	client := newRealtimeASRTestClient(t, websocketURL(server.URL), time.Second)
	_, err := client.Start(context.Background(), validASRConfig(), func(speech.TranscriptEvent) {})
	if !errors.Is(err, speech.ErrUpstreamProtocol) {
		t.Fatalf("Start error = %v, want ErrUpstreamProtocol", err)
	}
	if strings.Contains(err.Error(), "secret upstream detail") {
		t.Fatalf("Start error leaked provider message: %v", err)
	}
}

func TestRealtimeASRStartTimesOutWaitingForSession(t *testing.T) {
	releaseHandler := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-releaseHandler
	}))
	defer server.Close()
	defer close(releaseHandler)

	client := newRealtimeASRTestClient(t, websocketURL(server.URL), 25*time.Millisecond)
	_, err := client.Start(context.Background(), validASRConfig(), func(speech.TranscriptEvent) {})
	if !errors.Is(err, speech.ErrUpstreamTimeout) {
		t.Fatalf("Start error = %v, want ErrUpstreamTimeout", err)
	}
}

func TestRealtimeASRStartCancellationInterruptsHandshakeRead(t *testing.T) {
	releaseHandler := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-releaseHandler
	}))
	defer server.Close()
	defer close(releaseHandler)

	client := newRealtimeASRTestClient(t, websocketURL(server.URL), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	startedAt := time.Now()
	_, err := client.Start(ctx, validASRConfig(), func(speech.TranscriptEvent) {})
	if !errors.Is(err, speech.ErrClientCancelled) {
		t.Fatalf("Start error = %v, want ErrClientCancelled", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestRealtimeASRRejectsInvalidConfig(t *testing.T) {
	client := newRealtimeASRTestClient(t, "ws://127.0.0.1:1/ws", time.Second)
	_, err := client.Start(context.Background(), speech.ASRConfig{
		QuestionID: "q", SampleRate: 8000, Channels: 2, Format: "wav",
	}, func(speech.TranscriptEvent) {})
	if !errors.Is(err, speech.ErrInvalidRequest) {
		t.Fatalf("Start error = %v, want ErrInvalidRequest", err)
	}
}

func newRealtimeASRTestClient(t *testing.T, rawURL string, timeout time.Duration) *RealtimeASRClient {
	t.Helper()
	client, err := NewRealtimeASRClient(RealtimeASRConfig{
		APIKey:         "test-key",
		URL:            rawURL,
		Model:          "qwen3-asr-flash-realtime",
		ConnectTimeout: timeout,
		WriteTimeout:   time.Second,
		QueueSize:      4,
	})
	if err != nil {
		t.Fatalf("NewRealtimeASRClient: %v", err)
	}
	return client
}

func validASRConfig() speech.ASRConfig {
	return speech.ASRConfig{QuestionID: "question-1", SampleRate: speech.InputSampleRate, Channels: 1, Format: speech.InputFormatPCMS16LE}
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func mustReadProviderJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", data, err)
	}
	return value
}

func mustWriteProviderJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
}
