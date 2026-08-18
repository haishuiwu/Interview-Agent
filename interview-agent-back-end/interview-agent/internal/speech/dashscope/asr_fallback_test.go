package dashscope

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"interview-agent/internal/speech"
)

func TestFallbackASRTranscribesBase64WAV(t *testing.T) {
	wav, err := speech.PCMToWAV([]byte{1, 2, 3, 4}, speech.InputSampleRate, 1)
	if err != nil {
		t.Fatalf("PCMToWAV: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compatible-mode/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type       string `json:"type"`
					InputAudio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
			Stream     bool `json:"stream"`
			ASROptions struct {
				EnableITN bool `json:"enable_itn"`
			} `json:"asr_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "qwen3-asr-flash" || request.Stream || request.ASROptions.EnableITN {
			t.Errorf("request options = %+v", request)
		}
		if len(request.Messages) != 1 || request.Messages[0].Role != "user" || len(request.Messages[0].Content) != 1 || request.Messages[0].Content[0].Type != "input_audio" {
			t.Errorf("request messages = %+v", request.Messages)
			return
		}
		const prefix = "data:audio/wav;base64,"
		dataURL := request.Messages[0].Content[0].InputAudio.Data
		if !strings.HasPrefix(dataURL, prefix) {
			t.Errorf("audio data URL prefix missing")
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
		if err != nil {
			t.Errorf("decode audio: %v", err)
		} else if string(decoded) != string(wav) {
			t.Errorf("WAV payload changed")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fallback-1","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"  最终降级文本  "}}]}`))
	}))
	defer server.Close()

	client := newFallbackASRTestClient(t, server.URL+"/compatible-mode/v1", time.Second, 10*1024*1024, nil)
	event, err := client.TranscribeBuffered(context.Background(), wav)
	if err != nil {
		t.Fatalf("TranscribeBuffered: %v", err)
	}
	if event.Kind != "final" || event.Text != "最终降级文本" || !event.Degraded || event.ProviderRequestID != "chatcmpl-fallback-1" {
		t.Fatalf("event = %+v", event)
	}
}

func TestFallbackASRRejectsInvalidAndOversizedAudioBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client := newFallbackASRTestClient(t, server.URL, time.Second, 64, nil)

	if _, err := client.TranscribeBuffered(context.Background(), []byte("not wav")); !errors.Is(err, speech.ErrInvalidRequest) {
		t.Fatalf("invalid WAV error = %v", err)
	}
	wav, err := speech.PCMToWAV(make([]byte, 640), speech.InputSampleRate, 1)
	if err != nil {
		t.Fatalf("PCMToWAV: %v", err)
	}
	if _, err := client.TranscribeBuffered(context.Background(), wav); !errors.Is(err, speech.ErrInvalidRequest) {
		t.Fatalf("oversized WAV error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls.Load())
	}
}

func TestFallbackASRMapsTimeoutProviderAndProtocolErrors(t *testing.T) {
	wav, err := speech.PCMToWAV(make([]byte, 640), speech.InputSampleRate, 1)
	if err != nil {
		t.Fatalf("PCMToWAV: %v", err)
	}

	t.Run("timeout", func(t *testing.T) {
		releaseHandler := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-releaseHandler:
			}
		}))
		defer server.Close()
		defer close(releaseHandler)
		client := newFallbackASRTestClient(t, server.URL, 20*time.Millisecond, 10*1024*1024, nil)
		_, err := client.TranscribeBuffered(context.Background(), wav)
		if !errors.Is(err, speech.ErrUpstreamTimeout) {
			t.Fatalf("timeout error = %v", err)
		}
	})

	t.Run("provider", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"secret provider detail"}}`))
		}))
		defer server.Close()
		client := newFallbackASRTestClient(t, server.URL, time.Second, 10*1024*1024, nil)
		_, err := client.TranscribeBuffered(context.Background(), wav)
		if !errors.Is(err, speech.ErrUpstreamUnavailable) || strings.Contains(err.Error(), "secret provider detail") {
			t.Fatalf("provider error = %v", err)
		}
	})

	t.Run("protocol", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"request-without-choices"}`))
		}))
		defer server.Close()
		client := newFallbackASRTestClient(t, server.URL, time.Second, 10*1024*1024, nil)
		_, err := client.TranscribeBuffered(context.Background(), wav)
		if !errors.Is(err, speech.ErrUpstreamProtocol) {
			t.Fatalf("protocol error = %v", err)
		}
	})
}

func newFallbackASRTestClient(
	t *testing.T,
	baseURL string,
	timeout time.Duration,
	maxRequestBytes int,
	httpClient *http.Client,
) *FallbackASRClient {
	t.Helper()
	client, err := NewFallbackASRClient(FallbackASRConfig{
		APIKey:           "test-key",
		BaseURL:          baseURL,
		Model:            "qwen3-asr-flash",
		Timeout:          timeout,
		MaxRequestBytes:  maxRequestBytes,
		MaxResponseBytes: 1024,
		HTTPClient:       httpClient,
	})
	if err != nil {
		t.Fatalf("NewFallbackASRClient: %v", err)
	}
	return client
}
