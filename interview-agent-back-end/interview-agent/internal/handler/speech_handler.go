package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"interview-agent/internal/speech"
)

const maxTTSRequestBytes = 8 << 10

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
var providerHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

type ttsHTTPRequest struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
}

type speechErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func (s *Server) handleSpeechCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.authenticateHTTPRequest(r); err != nil {
		writeSpeechError(w, http.StatusUnauthorized, speech.ErrUnauthorized, "")
		return
	}

	capabilities := speech.DisabledCapabilities(180)
	if s.cfg.SpeechService != nil {
		capabilities = s.cfg.SpeechService.Capabilities()
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeSpeechJSON(w, http.StatusOK, capabilities)
}

func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	requestID := getSpeechRequestID(r)
	w.Header().Set("X-Request-ID", requestID)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeSpeechError(w, http.StatusMethodNotAllowed, speech.ErrInvalidRequest, requestID)
		return
	}
	userID, err := s.authenticateHTTPRequest(r)
	if err != nil {
		writeSpeechError(w, http.StatusUnauthorized, speech.ErrUnauthorized, requestID)
		return
	}
	if s.cfg.SpeechService == nil || !s.cfg.SpeechService.Capabilities().TTSEnabled {
		writeSpeechError(w, http.StatusServiceUnavailable, speech.ErrSpeechDisabled, requestID)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeSpeechError(w, http.StatusBadRequest, speech.ErrInvalidRequest, requestID)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTTSRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request ttsHTTPRequest
	if err := decoder.Decode(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeSpeechError(w, http.StatusRequestEntityTooLarge, speech.ErrInvalidRequest, requestID)
			return
		}
		writeSpeechError(w, http.StatusBadRequest, speech.ErrInvalidRequest, requestID)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeSpeechError(w, http.StatusRequestEntityTooLarge, speech.ErrInvalidRequest, requestID)
			return
		}
		writeSpeechError(w, http.StatusBadRequest, speech.ErrInvalidRequest, requestID)
		return
	}
	request.QuestionID = strings.TrimSpace(request.QuestionID)
	if request.QuestionID == "" || len(request.QuestionID) > 64 || strings.IndexFunc(request.QuestionID, unicode.IsControl) >= 0 || strings.TrimSpace(request.Text) == "" {
		writeSpeechError(w, http.StatusBadRequest, speech.ErrInvalidRequest, requestID)
		return
	}
	startedAt := time.Now()
	event := speech.Event{
		Name: speech.EventTTSStarted, RequestID: requestID, QuestionID: request.QuestionID,
		UserID: userID, Provider: s.cfg.SpeechProvider, Model: s.cfg.SpeechTTSModel,
		TextChars: utf8.RuneCountInString(request.Text),
	}
	s.logSpeechEvent(r.Context(), event)

	result, err := s.cfg.SpeechService.Synthesize(r.Context(), userID, speech.TTSRequest{
		RequestID:  requestID,
		QuestionID: request.QuestionID,
		Text:       request.Text,
	})
	if err != nil {
		status, publicError := mapTTSError(err)
		event.DurationMS = time.Since(startedAt).Milliseconds()
		event.ErrorCode = publicError.Code
		switch {
		case errors.Is(publicError, speech.ErrClientCancelled):
			event.Name = speech.EventSessionCancelled
		case errors.Is(err, speech.ErrTTSAlreadyRunning), errors.Is(err, speech.ErrSpeechBusy), errors.Is(err, speech.ErrLimitExceeded):
			event.Name = speech.EventLimitRejected
		default:
			event.Name = speech.EventTTSFailed
		}
		s.logSpeechEvent(r.Context(), event)
		if errors.Is(publicError, speech.ErrClientCancelled) {
			return
		}
		writeSpeechError(w, status, publicError, requestID)
		return
	}
	defer result.Audio.Close()

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, no-store")
	if providerHeaderPattern.MatchString(result.Provider) {
		w.Header().Set("X-Speech-Provider", result.Provider)
	}
	if result.SizeHint > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.SizeHint, 10))
	}
	w.WriteHeader(http.StatusOK)
	written, copyErr := io.Copy(w, result.Audio)
	event.DurationMS = time.Since(startedAt).Milliseconds()
	event.Provider = result.Provider
	event.ProviderRequestID = result.ProviderRequestID
	event.AudioBytes = int(written)
	if copyErr != nil {
		event.Name = speech.EventSessionCancelled
		event.ErrorCode = speech.CodeClientCancelled
		s.logSpeechEvent(r.Context(), event)
		return
	}
	event.Name = speech.EventTTSCompleted
	s.logSpeechEvent(r.Context(), event)
}

func getSpeechRequestID(r *http.Request) string {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestIDPattern.MatchString(requestID) {
		return requestID
	}
	return uuid.NewString()
}

func mapTTSError(err error) (int, *speech.Error) {
	switch {
	case errors.Is(err, speech.ErrInvalidRequest):
		return http.StatusBadRequest, speech.ErrInvalidRequest
	case errors.Is(err, speech.ErrTextTooLong):
		return http.StatusRequestEntityTooLarge, speech.ErrTextTooLong
	case errors.Is(err, speech.ErrTTSAlreadyRunning):
		return http.StatusConflict, speech.ErrTTSAlreadyRunning
	case errors.Is(err, speech.ErrSpeechBusy), errors.Is(err, speech.ErrLimitExceeded):
		return http.StatusTooManyRequests, speech.ErrSpeechBusy
	case errors.Is(err, speech.ErrSpeechDisabled):
		return http.StatusServiceUnavailable, speech.ErrSpeechDisabled
	case errors.Is(err, speech.ErrTTSUpstreamTimeout):
		return http.StatusGatewayTimeout, speech.ErrTTSUpstreamTimeout
	case errors.Is(err, speech.ErrClientCancelled):
		return 0, speech.ErrClientCancelled
	default:
		return http.StatusBadGateway, speech.ErrTTSUpstreamError
	}
}

func writeSpeechError(w http.ResponseWriter, status int, speechErr *speech.Error, requestID string) {
	response := speechErrorBody{}
	response.Error.Code = speechErr.Code
	response.Error.Message = speechErr.Message
	response.RequestID = requestID
	writeSpeechJSON(w, status, response)
}

func writeSpeechJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("[Speech] JSON 响应写入失败: %v", err)
		return
	}
}
