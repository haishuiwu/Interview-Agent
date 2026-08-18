package speech

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	EventTTSStarted          = "speech_tts_started"
	EventTTSCompleted        = "speech_tts_completed"
	EventTTSFailed           = "speech_tts_failed"
	EventASRStarted          = "speech_asr_started"
	EventASRFirstPartial     = "speech_asr_first_partial"
	EventASRRealtimeDegraded = "speech_asr_realtime_degraded"
	EventASRFallbackStarted  = "speech_asr_fallback_started"
	EventASRCompleted        = "speech_asr_completed"
	EventASRFailed           = "speech_asr_failed"
	EventSessionCancelled    = "speech_session_cancelled"
	EventLimitRejected       = "speech_limit_rejected"
)

// Event contains only bounded metadata. Audio, complete transcript text,
// credentials, JWTs and signed URLs deliberately have no fields here.
type Event struct {
	Name              string
	RequestID         string
	QuestionID        string
	UserID            string
	Provider          string
	Model             string
	ProviderRequestID string
	DurationMS        int64
	FirstResultMS     int64
	AudioBytes        int
	TextChars         int
	Degraded          bool
	ErrorCode         string
}

type EventLogger interface {
	LogSpeechEvent(ctx context.Context, event Event)
}

// JSONEventLogger writes one slog JSON object per speech lifecycle event. The
// user identifier is HMACed so logs remain correlatable without exposing the
// authenticated identifier itself.
type JSONEventLogger struct {
	logger  *slog.Logger
	hashKey []byte
}

func NewJSONEventLogger(writer io.Writer, userHashKey string) *JSONEventLogger {
	if writer == nil {
		writer = io.Discard
	}
	key := sha256.Sum256([]byte("interview-agent/speech-log/" + userHashKey))
	return &JSONEventLogger{
		logger:  slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})),
		hashKey: key[:],
	}
}

func (l *JSONEventLogger) LogSpeechEvent(ctx context.Context, event Event) {
	if l == nil || l.logger == nil {
		return
	}
	attributes := []any{
		"event", sanitizeLogValue(event.Name, 64),
		"duration_ms", nonNegativeInt64(event.DurationMS),
		"first_result_ms", nonNegativeInt64(event.FirstResultMS),
		"audio_bytes", nonNegativeInt(event.AudioBytes),
		"text_chars", nonNegativeInt(event.TextChars),
		"degraded", event.Degraded,
	}
	if value := sanitizeLogValue(event.RequestID, 64); value != "" {
		attributes = append(attributes, "request_id", value)
	}
	if value := sanitizeLogValue(event.QuestionID, 64); value != "" {
		attributes = append(attributes, "question_id", value)
	}
	if event.UserID != "" {
		attributes = append(attributes, "user_id_hash", l.userHash(event.UserID))
	}
	if value := sanitizeLogValue(event.Provider, 32); value != "" {
		attributes = append(attributes, "provider", value)
	}
	if value := sanitizeLogValue(event.Model, 64); value != "" {
		attributes = append(attributes, "model", value)
	}
	if value := sanitizeLogValue(event.ProviderRequestID, 128); value != "" {
		attributes = append(attributes, "provider_request_id", value)
	}
	if value := sanitizeLogValue(event.ErrorCode, 64); value != "" {
		attributes = append(attributes, "error_code", value)
	}
	l.logger.InfoContext(ctx, "speech_event", attributes...)
}

func (l *JSONEventLogger) userHash(userID string) string {
	digest := hmac.New(sha256.New, l.hashKey)
	_, _ = digest.Write([]byte(userID))
	return hex.EncodeToString(digest.Sum(nil)[:16])
}

func sanitizeLogValue(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if len(value) > maxBytes {
		value = value[:maxBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

var _ EventLogger = (*JSONEventLogger)(nil)
