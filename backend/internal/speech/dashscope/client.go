// Package dashscope contains the Alibaba Cloud DashScope protocol adapters.
package dashscope

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderName = "dashscope"
	ttsPath      = "/services/aigc/multimodal-generation/generation"
)

var errUntrustedAudioURL = errors.New("untrusted audio URL")

// ClientConfig configures the non-streaming Qwen TTS HTTP adapter.
type ClientConfig struct {
	APIKey            string
	BaseURL           string
	Model             string
	Voice             string
	Language          string
	Timeout           time.Duration
	MaxAudioBytes     int64
	AllowedAudioHosts []string
	HTTPClient        *http.Client
	DownloadClient    *http.Client
	RetryDelay        func(context.Context) error
}

// Client implements speech.Synthesizer using DashScope's non-streaming API.
type Client struct {
	apiKey            string
	endpoint          string
	model             string
	voice             string
	language          string
	timeout           time.Duration
	maxAudioBytes     int64
	allowedAudioHosts []string
	apiClient         *http.Client
	downloadClient    *http.Client
	retryDelay        func(context.Context) error
}

// DefaultAllowedAudioHosts returns the Alibaba OSS suffixes used by current
// DashScope result URLs. Suffix matches also require a dashscope-result-* host.
func DefaultAllowedAudioHosts() []string {
	return []string{
		".oss-cn-beijing.aliyuncs.com",
		".oss-cn-wulanchabu.aliyuncs.com",
		".oss-ap-southeast-1.aliyuncs.com",
		".oss-accelerate.aliyuncs.com",
	}
}

// NewClient creates isolated API/download HTTP clients. It performs no network
// operation, so disabled feature flags never initialize an upstream connection.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("dashscope TTS: API key is required")
	}
	baseURL, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || baseURL.Host == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.User != nil {
		return nil, fmt.Errorf("dashscope TTS: invalid base URL")
	}
	if strings.TrimSpace(cfg.Model) == "" || strings.TrimSpace(cfg.Voice) == "" || strings.TrimSpace(cfg.Language) == "" {
		return nil, fmt.Errorf("dashscope TTS: model, voice and language are required")
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("dashscope TTS: timeout must be positive")
	}
	if cfg.MaxAudioBytes <= 0 {
		return nil, fmt.Errorf("dashscope TTS: max audio bytes must be positive")
	}
	if len(cfg.AllowedAudioHosts) == 0 {
		return nil, fmt.Errorf("dashscope TTS: allowed audio hosts are required")
	}

	apiClient := cloneHTTPClient(cfg.HTTPClient)
	apiClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	downloadClient := cloneHTTPClient(cfg.DownloadClient)

	client := &Client{
		apiKey:            cfg.APIKey,
		endpoint:          strings.TrimRight(baseURL.String(), "/") + ttsPath,
		model:             cfg.Model,
		voice:             cfg.Voice,
		language:          cfg.Language,
		timeout:           cfg.Timeout,
		maxAudioBytes:     cfg.MaxAudioBytes,
		allowedAudioHosts: append([]string(nil), cfg.AllowedAudioHosts...),
		apiClient:         apiClient,
		downloadClient:    downloadClient,
		retryDelay:        cfg.RetryDelay,
	}
	if client.retryDelay == nil {
		client.retryDelay = defaultRetryDelay
	}
	client.downloadClient.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if !client.isAllowedAudioURL(req.URL) {
			return errUntrustedAudioURL
		}
		return nil
	}
	return client, nil
}

func cloneHTTPClient(source *http.Client) *http.Client {
	if source == nil {
		return &http.Client{}
	}
	clone := *source
	return &clone
}

func defaultRetryDelay(ctx context.Context) error {
	jitter, err := rand.Int(rand.Reader, big.NewInt(201))
	if err != nil {
		jitter = big.NewInt(100)
	}
	delay := time.Duration(100+jitter.Int64()) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) isAllowedAudioURL(candidate *url.URL) bool {
	if candidate == nil || (candidate.Scheme != "http" && candidate.Scheme != "https") || candidate.User != nil || candidate.Hostname() == "" || candidate.Fragment != "" {
		return false
	}
	host := strings.ToLower(candidate.Hostname())
	for _, allowed := range c.allowedAudioHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(allowed, ".") {
			if strings.HasPrefix(host, "dashscope-result-") && strings.HasSuffix(host, allowed) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}
