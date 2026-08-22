package dashscope

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"interview-agent/internal/speech"
)

const (
	defaultFallbackTimeout            = 45 * time.Second
	defaultFallbackMaxRequestBytes    = 10 * 1024 * 1024
	defaultFallbackMaxResponseBytes   = 1024 * 1024
	fallbackAudioDataURLPrefix        = "data:audio/wav;base64,"
	fallbackRequestSizeSafetyOverhead = 1024
)

type FallbackASRConfig struct {
	APIKey           string
	BaseURL          string
	Model            string
	Timeout          time.Duration
	MaxRequestBytes  int
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

// FallbackASRClient implements the bounded, non-streaming OpenAI-compatible
// qwen3-asr-flash request used only after the realtime path degrades.
type FallbackASRClient struct {
	apiKey           string
	endpoint         string
	model            string
	timeout          time.Duration
	maxRequestBytes  int
	maxResponseBytes int64
	httpClient       *http.Client
}

type fallbackASRRequest struct {
	Model    string               `json:"model"`
	Messages []fallbackASRMessage `json:"messages"`
	Stream   bool                 `json:"stream"`
	Options  fallbackASROptions   `json:"asr_options"`
}

type fallbackASRMessage struct {
	Role    string               `json:"role"`
	Content []fallbackASRContent `json:"content"`
}

type fallbackASRContent struct {
	Type       string                `json:"type"`
	InputAudio fallbackASRAudioInput `json:"input_audio"`
}

type fallbackASRAudioInput struct {
	Data string `json:"data"`
}

type fallbackASROptions struct {
	EnableITN bool `json:"enable_itn"`
}

type fallbackASRResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func NewFallbackASRClient(cfg FallbackASRConfig) (*FallbackASRClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("dashscope fallback ASR: API key is required")
	}
	endpoint, err := fallbackASREndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("dashscope fallback ASR: model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultFallbackTimeout
	}
	if cfg.MaxRequestBytes <= 0 {
		cfg.MaxRequestBytes = defaultFallbackMaxRequestBytes
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = defaultFallbackMaxResponseBytes
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &FallbackASRClient{
		apiKey:           strings.TrimSpace(cfg.APIKey),
		endpoint:         endpoint,
		model:            strings.TrimSpace(cfg.Model),
		timeout:          cfg.Timeout,
		maxRequestBytes:  cfg.MaxRequestBytes,
		maxResponseBytes: cfg.MaxResponseBytes,
		httpClient:       cfg.HTTPClient,
	}, nil
}

func fallbackASREndpoint(baseURL string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Fragment != "" || endpoint.RawQuery != "" {
		return "", fmt.Errorf("dashscope fallback ASR: invalid base URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/chat/completions"
	endpoint.RawPath = ""
	return endpoint.String(), nil
}

func (c *FallbackASRClient) TranscribeBuffered(ctx context.Context, wav []byte) (speech.TranscriptEvent, error) {
	if c == nil || !speech.IsWAV(wav) || len(wav) < pcmWAVHeaderBytesForFallback {
		return speech.TranscriptEvent{}, speech.ErrInvalidRequest
	}
	encodedBytes := base64.StdEncoding.EncodedLen(len(wav))
	if encodedBytes+len(fallbackAudioDataURLPrefix)+fallbackRequestSizeSafetyOverhead > c.maxRequestBytes {
		return speech.TranscriptEvent{}, speech.ErrInvalidRequest
	}
	dataURL := fallbackAudioDataURLPrefix + base64.StdEncoding.EncodeToString(wav)
	payload, err := json.Marshal(fallbackASRRequest{
		Model: c.model,
		Messages: []fallbackASRMessage{{
			Role: "user",
			Content: []fallbackASRContent{{
				Type:       "input_audio",
				InputAudio: fallbackASRAudioInput{Data: dataURL},
			}},
		}},
		Stream:  false,
		Options: fallbackASROptions{EnableITN: false},
	})
	if err != nil {
		return speech.TranscriptEvent{}, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("encode fallback ASR request"))
	}
	if len(payload) > c.maxRequestBytes {
		return speech.TranscriptEvent{}, speech.ErrInvalidRequest
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return speech.TranscriptEvent{}, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("create fallback ASR request"))
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Student-Coach-Go/1.0")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return speech.TranscriptEvent{}, mapFallbackTransportError(ctx, requestCtx, err)
	}
	defer response.Body.Close()
	responseData, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if readErr != nil {
		return speech.TranscriptEvent{}, mapFallbackTransportError(ctx, requestCtx, readErr)
	}
	if int64(len(responseData)) > c.maxResponseBytes {
		return speech.TranscriptEvent{}, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("fallback ASR response too large"))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return speech.TranscriptEvent{}, mapFallbackStatus(response.StatusCode)
	}

	var result fallbackASRResponse
	if err := json.Unmarshal(responseData, &result); err != nil {
		return speech.TranscriptEvent{}, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("invalid fallback ASR response"))
	}
	if len(result.Choices) != 1 || result.Choices[0].FinishReason != "stop" {
		return speech.TranscriptEvent{}, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("missing fallback ASR choice"))
	}
	text := strings.TrimSpace(result.Choices[0].Message.Content)
	if text == "" {
		return speech.TranscriptEvent{}, speech.ErrEmptyAudio
	}
	return speech.TranscriptEvent{
		Kind:              "final",
		Text:              text,
		Degraded:          true,
		ProviderRequestID: boundedProviderRequestID(result.ID),
	}, nil
}

const pcmWAVHeaderBytesForFallback = 44

func boundedProviderRequestID(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if len(requestID) > 128 {
		requestID = requestID[:128]
	}
	return requestID
}

func mapFallbackTransportError(parent, request context.Context, err error) error {
	if errors.Is(parent.Err(), context.Canceled) {
		return speech.WithCause(speech.ErrClientCancelled, errors.New("fallback ASR cancelled"))
	}
	if errors.Is(request.Err(), context.DeadlineExceeded) {
		return speech.WithCause(speech.ErrUpstreamTimeout, errors.New("fallback ASR timed out"))
	}
	return speech.WithCause(speech.ErrUpstreamUnavailable, fmt.Errorf("fallback ASR transport: %T", err))
}

func mapFallbackStatus(status int) error {
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return speech.WithCause(speech.ErrUpstreamTimeout, fmt.Errorf("fallback ASR HTTP status %d", status))
	case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError:
		return speech.WithCause(speech.ErrUpstreamUnavailable, fmt.Errorf("fallback ASR HTTP status %d", status))
	default:
		return speech.WithCause(speech.ErrUpstreamProtocol, fmt.Errorf("fallback ASR HTTP status %d", status))
	}
}
