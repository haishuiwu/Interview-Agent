package dashscope

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"interview-agent/internal/speech"
)

func TestClientSynthesizeDownloadsWAV(t *testing.T) {
	wav := testWAV("audio")
	audioServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer audioServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Model string `json:"model"`
			Input struct {
				Text         string `json:"text"`
				Voice        string `json:"voice"`
				LanguageType string `json:"language_type"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "qwen3-tts-flash" || body.Input.Text != "你好 Go" || body.Input.Voice != "Cherry" || body.Input.LanguageType != "Auto" {
			t.Errorf("unexpected request: %+v", body)
		}
		writeAPIResponse(t, w, http.StatusOK, 200, "provider-request", "", "", audioServer.URL+"/result.wav")
	}))
	defer apiServer.Close()

	client := newTestClient(t, apiServer.URL+"/api/v1", []string{hostOf(t, audioServer.URL)}, 1024, time.Second)
	result, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "你好 Go"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	defer result.Audio.Close()
	got, err := io.ReadAll(result.Audio)
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	if string(got) != string(wav) {
		t.Fatalf("audio = %q, want %q", got, wav)
	}
	if result.Provider != ProviderName || result.ProviderRequestID != "provider-request" || result.ContentType != "audio/wav" {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
}

func TestClientSynthesizeMapsDashScopeError(t *testing.T) {
	var calls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeAPIResponse(t, w, http.StatusBadRequest, 400, "provider-request", "InvalidParameter", "do not expose", "")
	}))
	defer apiServer.Close()

	client := newTestClient(t, apiServer.URL, []string{"127.0.0.1"}, 1024, time.Second)
	_, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrUpstreamUnavailable) {
		t.Fatalf("Synthesize error = %v, want ErrUpstreamUnavailable", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("valid upstream error response was retried %d times", calls.Load())
	}
}

func TestClientSynthesizeRetriesOneTransportFailure(t *testing.T) {
	wav := testWAV("audio")
	audioServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(wav)
	}))
	defer audioServer.Close()
	apiServer := apiServerForAudioURL(t, audioServer.URL+"/audio.wav")
	defer apiServer.Close()

	var calls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("temporary transport failure")
		}
		return http.DefaultTransport.RoundTrip(req)
	})
	client, err := NewClient(ClientConfig{
		APIKey:            "test-key",
		BaseURL:           apiServer.URL,
		Model:             "qwen3-tts-flash",
		Voice:             "Cherry",
		Language:          "Auto",
		Timeout:           time.Second,
		MaxAudioBytes:     1024,
		AllowedAudioHosts: []string{hostOf(t, audioServer.URL)},
		HTTPClient:        &http.Client{Transport: transport},
		RetryDelay:        func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	result, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	_ = result.Audio.Close()
	if calls.Load() != 2 {
		t.Fatalf("transport calls = %d, want 2", calls.Load())
	}
}

func TestClientSynthesizeTimesOut(t *testing.T) {
	releaseHandler := make(chan struct{})
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	}))
	defer apiServer.Close()
	defer close(releaseHandler)

	client := newTestClient(t, apiServer.URL, []string{"127.0.0.1"}, 1024, 30*time.Millisecond)
	_, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrUpstreamTimeout) {
		t.Fatalf("Synthesize error = %v, want ErrUpstreamTimeout", err)
	}
}

func TestClientSynthesizeRejectsOversizedAudio(t *testing.T) {
	audioServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testWAV(strings.Repeat("x", 64)))
	}))
	defer audioServer.Close()
	apiServer := apiServerForAudioURL(t, audioServer.URL+"/large.wav")
	defer apiServer.Close()

	client := newTestClient(t, apiServer.URL, []string{hostOf(t, audioServer.URL)}, 20, time.Second)
	_, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrUpstreamProtocol) {
		t.Fatalf("Synthesize error = %v, want ErrUpstreamProtocol", err)
	}
}

func TestClientSynthesizeRejectsUntrustedAudioURL(t *testing.T) {
	apiServer := apiServerForAudioURL(t, "https://evil.example/signed.wav?secret=value")
	defer apiServer.Close()

	client := newTestClient(t, apiServer.URL, []string{"dashscope-result-bj.oss-cn-beijing.aliyuncs.com"}, 1024, time.Second)
	_, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrUpstreamProtocol) {
		t.Fatalf("Synthesize error = %v, want ErrUpstreamProtocol", err)
	}
}

func TestClientSynthesizeRejectsRedirectToUntrustedHost(t *testing.T) {
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testWAV("should-not-download"))
	}))
	defer targetServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetURL := strings.Replace(targetServer.URL, "127.0.0.1", "localhost", 1)
		http.Redirect(w, r, targetURL+"/audio.wav", http.StatusFound)
	}))
	defer redirectServer.Close()
	apiServer := apiServerForAudioURL(t, redirectServer.URL+"/redirect")
	defer apiServer.Close()

	client := newTestClient(t, apiServer.URL, []string{"127.0.0.1"}, 1024, time.Second)
	_, err := client.Synthesize(context.Background(), speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrUpstreamProtocol) {
		t.Fatalf("Synthesize error = %v, want ErrUpstreamProtocol", err)
	}
}

func TestClientSynthesizeHonorsClientCancellation(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer apiServer.Close()
	client := newTestClient(t, apiServer.URL, []string{"127.0.0.1"}, 1024, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Synthesize(ctx, speech.TTSRequest{Text: "hello"})
	if !errors.Is(err, speech.ErrClientCancelled) {
		t.Fatalf("Synthesize error = %v, want ErrClientCancelled", err)
	}
}

func newTestClient(t *testing.T, baseURL string, allowedHosts []string, maxAudioBytes int64, timeout time.Duration) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		APIKey:            "test-key",
		BaseURL:           baseURL,
		Model:             "qwen3-tts-flash",
		Voice:             "Cherry",
		Language:          "Auto",
		Timeout:           timeout,
		MaxAudioBytes:     maxAudioBytes,
		AllowedAudioHosts: allowedHosts,
		RetryDelay:        func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func apiServerForAudioURL(t *testing.T, audioURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIResponse(t, w, http.StatusOK, 200, "provider-request", "", "", audioURL)
	}))
}

func writeAPIResponse(t *testing.T, w http.ResponseWriter, httpStatus, statusCode int, requestID, code, message, audioURL string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status_code": statusCode,
		"request_id":  requestID,
		"code":        code,
		"message":     message,
		"output": map[string]any{
			"audio": map[string]any{"url": audioURL},
		},
	}); err != nil {
		t.Errorf("encode API response: %v", err)
	}
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed.Hostname()
}

func testWAV(payload string) []byte {
	return append([]byte("RIFF\x00\x00\x00\x00WAVE"), []byte(payload)...)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
