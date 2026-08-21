// Package speech defines provider-neutral speech capabilities for StudentCoach.
package speech

import (
	"context"
	"io"
)

const (
	InputFormatPCMS16LE = "pcm_s16le"
	InputSampleRate     = 16000
)

// TTSRequest is the provider-neutral text-to-speech input.
type TTSRequest struct {
	RequestID  string
	QuestionID string
	Text       string
	Voice      string
	Language   string
}

// TTSResult contains a streaming audio response owned by the caller.
type TTSResult struct {
	Audio             io.ReadCloser
	ContentType       string
	Provider          string
	ProviderRequestID string
	SizeHint          int64
}

// Synthesizer is implemented by a TTS provider adapter.
type Synthesizer interface {
	Synthesize(ctx context.Context, req TTSRequest) (*TTSResult, error)
}

// ASRConfig is the provider-neutral streaming recognition configuration.
type ASRConfig struct {
	QuestionID string
	SampleRate int
	Channels   int
	Format     string
}

// TranscriptEvent is a normalized ASR provider event.
type TranscriptEvent struct {
	Kind              string
	Seq               uint64
	Text              string
	Degraded          bool
	ProviderRequestID string
}

// ASRStream is an active streaming recognition session.
type ASRStream interface {
	WriteAudio(ctx context.Context, pcm []byte) error
	Finish(ctx context.Context) (TranscriptEvent, error)
	Close() error
}

// Transcriber is implemented by an ASR provider adapter.
type Transcriber interface {
	Start(ctx context.Context, cfg ASRConfig, onEvent func(TranscriptEvent)) (ASRStream, error)
	TranscribeBuffered(ctx context.Context, wav []byte) (TranscriptEvent, error)
}

// Capabilities is safe to expose to authenticated clients. It deliberately
// contains no provider credentials or upstream endpoints.
type Capabilities struct {
	Enabled          bool   `json:"enabled"`
	TTSEnabled       bool   `json:"tts_enabled"`
	ASREnabled       bool   `json:"asr_enabled"`
	MaxAnswerSeconds int    `json:"max_answer_seconds"`
	InputFormat      string `json:"input_format"`
	InputSampleRate  int    `json:"input_sample_rate"`
}

// DisabledCapabilities returns the stable response used before a speech
// service/provider is configured.
func DisabledCapabilities(maxAnswerSeconds int) Capabilities {
	if maxAnswerSeconds <= 0 {
		maxAnswerSeconds = 180
	}
	return Capabilities{
		MaxAnswerSeconds: maxAnswerSeconds,
		InputFormat:      InputFormatPCMS16LE,
		InputSampleRate:  InputSampleRate,
	}
}
