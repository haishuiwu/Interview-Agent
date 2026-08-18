package dashscope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"interview-agent/internal/speech"
)

const maxAPIResponseBytes = 1 << 20

type ttsAPIRequest struct {
	Model string `json:"model"`
	Input struct {
		Text         string `json:"text"`
		Voice        string `json:"voice"`
		LanguageType string `json:"language_type,omitempty"`
	} `json:"input"`
}

type ttsAPIResponse struct {
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Output     struct {
		Audio struct {
			URL string `json:"url"`
		} `json:"audio"`
	} `json:"output"`
}

// Synthesize calls DashScope, validates the signed result URL, downloads a
// bounded WAV file and returns provider-neutral audio bytes.
func (c *Client) Synthesize(ctx context.Context, req speech.TTSRequest) (*speech.TTSResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	apiResponse, err := c.requestAudioURL(callCtx, req)
	if err != nil {
		return nil, c.classifyContextError(ctx, callCtx, err)
	}
	result, err := c.downloadAudio(callCtx, apiResponse.Output.Audio.URL, apiResponse.RequestID)
	if err != nil {
		return nil, c.classifyContextError(ctx, callCtx, err)
	}
	return result, nil
}

func (c *Client) requestAudioURL(ctx context.Context, req speech.TTSRequest) (*ttsAPIResponse, error) {
	payload := ttsAPIRequest{Model: c.model}
	payload.Input.Text = req.Text
	payload.Input.Voice = c.voice
	payload.Input.LanguageType = c.language
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("encode request"))
	}

	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		httpReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("create request"))
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		response, err = c.apiClient.Do(httpReq)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == 0 {
			if delayErr := c.retryDelay(ctx); delayErr != nil {
				return nil, delayErr
			}
		}
	}
	if err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("DashScope request failed"))
	}
	defer response.Body.Close()

	limitedBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponseBytes+1))
	if err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("read DashScope response"))
	}
	if len(limitedBody) > maxAPIResponseBytes {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("DashScope response too large"))
	}
	var parsed ttsAPIResponse
	if err := json.Unmarshal(limitedBody, &parsed); err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("decode DashScope response"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || parsed.StatusCode != http.StatusOK || parsed.Code != "" {
		cause := fmt.Errorf("DashScope status=%d code=%s request_id=%s", parsed.StatusCode, parsed.Code, parsed.RequestID)
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, cause)
	}
	if parsed.Output.Audio.URL == "" {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("DashScope response missing audio URL"))
	}
	return &parsed, nil
}

func (c *Client) downloadAudio(ctx context.Context, rawURL, providerRequestID string) (*speech.TTSResult, error) {
	audioURL, err := url.Parse(rawURL)
	if err != nil || !c.isAllowedAudioURL(audioURL) {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("audio URL rejected"))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL.String(), nil)
	if err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("create audio request"))
	}
	response, err := c.downloadClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, errUntrustedAudioURL) {
			return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("audio redirect rejected"))
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("audio download failed"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, fmt.Errorf("audio download status=%d", response.StatusCode))
	}
	if response.ContentLength > c.maxAudioBytes {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("audio exceeds size limit"))
	}
	audio, err := io.ReadAll(io.LimitReader(response.Body, c.maxAudioBytes+1))
	if err != nil {
		return nil, speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("read audio response"))
	}
	if int64(len(audio)) > c.maxAudioBytes {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("audio exceeds size limit"))
	}
	if !speech.IsWAV(audio) {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("audio is not WAV"))
	}
	return &speech.TTSResult{
		Audio:             io.NopCloser(bytes.NewReader(audio)),
		ContentType:       "audio/wav",
		Provider:          ProviderName,
		ProviderRequestID: providerRequestID,
		SizeHint:          int64(len(audio)),
	}, nil
}

func (c *Client) classifyContextError(parent, call context.Context, err error) error {
	if parent.Err() != nil {
		return speech.WithCause(speech.ErrClientCancelled, errors.New("client context cancelled"))
	}
	if errors.Is(call.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return speech.WithCause(speech.ErrUpstreamTimeout, errors.New("DashScope TTS timed out"))
	}
	return err
}
