package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"interview-agent/internal/speech"
)

const (
	maxASRControlBytes = 4 * 1024
	maxASRFrameBytes   = 64 * 1024
	minASRAudioBytes   = 640
	maxASRViolations   = 3
	asrDegradedCode    = "REALTIME_DEGRADED"
	asrDegradedMessage = "实时字幕暂时不可用，录音结束后将继续识别"
)

type asrConnectionState uint8

const (
	asrStateConnected asrConnectionState = iota
	asrStateStarting
	asrStateReady
	asrStateStreaming
	asrStateStopping
)

type asrClientControl struct {
	Type       string `json:"type"`
	QuestionID string `json:"question_id"`
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

type asrServerMessage struct {
	Type              string `json:"type"`
	QuestionID        string `json:"question_id,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	Seq               uint64 `json:"seq,omitempty"`
	Text              string `json:"text,omitempty"`
	Degraded          bool   `json:"degraded,omitempty"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
}

type asrClientFrame struct {
	messageType int
	data        []byte
}

type asrStartResult struct {
	err error
}

type asrFinishResult struct {
	event    speech.TranscriptEvent
	err      error
	fallback bool
}

type asrWebSocketSession struct {
	conn               *websocket.Conn
	reservation        *speech.ASRReservation
	questions          *activeQuestionRegistry
	userID             string
	requestID          string
	questionID         string
	state              asrConnectionState
	lastSeq            uint64
	violations         int
	degraded           bool
	warningSent        bool
	fallbackStarted    bool
	buffer             *speech.PCMBuffer
	maxAudioBytes      int
	logger             speech.EventLogger
	provider           string
	realtimeModel      string
	fallbackModel      string
	startedAt          time.Time
	firstPartialLogged bool
	terminalLogged     bool

	ctx            context.Context
	cancel         context.CancelFunc
	frames         chan asrClientFrame
	readErrors     chan error
	providerEvents chan speech.TranscriptEvent
	startResults   chan asrStartResult
	finishResults  chan asrFinishResult
	wg             sync.WaitGroup
}

func (s *Server) handleASRWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isASROriginAllowed(r) {
		http.Error(w, "Forbidden Origin", http.StatusForbidden)
		return
	}
	userID, err := s.authenticateWebSocketRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	requestID := uuid.NewString()
	if s.cfg.SpeechService == nil || !s.cfg.SpeechService.Capabilities().ASREnabled {
		http.Error(w, "Speech ASR Disabled", http.StatusServiceUnavailable)
		return
	}
	reservation, err := s.cfg.SpeechService.ReserveASR(userID)
	if err != nil {
		if errors.Is(err, speech.ErrLimitExceeded) {
			s.logSpeechEvent(r.Context(), speech.Event{
				Name: speech.EventLimitRejected, RequestID: requestID, UserID: userID,
				Provider: s.cfg.SpeechProvider, Model: s.cfg.SpeechASRRealtimeModel,
				ErrorCode: speech.CodeLimitExceeded,
			})
			http.Error(w, "Speech ASR Busy", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "Speech ASR Disabled", http.StatusServiceUnavailable)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4 * 1024,
		WriteBufferSize: 4 * 1024,
		CheckOrigin:     s.isASROriginAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = reservation.Close()
		return
	}
	buffer, err := speech.NewPCMBuffer(reservation.MaxASRAudioBytes())
	if err != nil {
		_ = conn.Close()
		_ = reservation.Close()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &asrWebSocketSession{
		conn:           conn,
		reservation:    reservation,
		questions:      s.speechQuestions,
		userID:         userID,
		requestID:      requestID,
		state:          asrStateConnected,
		buffer:         buffer,
		maxAudioBytes:  reservation.MaxASRAudioBytes(),
		logger:         s.cfg.SpeechLogger,
		provider:       s.cfg.SpeechProvider,
		realtimeModel:  s.cfg.SpeechASRRealtimeModel,
		fallbackModel:  s.cfg.SpeechASRFallbackModel,
		ctx:            ctx,
		cancel:         cancel,
		frames:         make(chan asrClientFrame, 8),
		readErrors:     make(chan error, 1),
		providerEvents: make(chan speech.TranscriptEvent, 32),
		startResults:   make(chan asrStartResult, 1),
		finishResults:  make(chan asrFinishResult, 1),
	}
	session.run()
}

func (s *Server) isASROriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return len(s.cfg.AllowedOrigins) == 0
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func (s *asrWebSocketSession) run() {
	defer func() {
		if !s.startedAt.IsZero() && !s.terminalLogged {
			s.logEvent(speech.Event{
				Name: speech.EventSessionCancelled, ErrorCode: speech.CodeClientCancelled,
			})
			s.terminalLogged = true
		}
		s.cancel()
		_ = s.conn.Close()
		_ = s.reservation.Close()
		s.wg.Wait()
		s.buffer.Release()
	}()
	s.conn.SetReadLimit(maxASRFrameBytes)
	s.wg.Add(1)
	go s.readPump()

	for {
		select {
		case frame := <-s.frames:
			if s.handleClientFrame(frame) {
				s.writeClose(websocket.CloseNormalClosure, "")
				return
			}
		case result := <-s.startResults:
			if result.err != nil {
				if !shouldFallbackASR(result.err) {
					s.sendError(publicASRError(result.err))
					return
				}
				if s.state != asrStateStarting || !s.questions.Match(s.userID, s.questionID) {
					s.sendError(speech.ErrInvalidRequest)
					return
				}
				s.state = asrStateReady
				if err := s.sendReady(); err != nil {
					return
				}
				if err := s.enterFallbackMode(result.err); err != nil {
					return
				}
				continue
			}
			if s.state != asrStateStarting || !s.questions.Match(s.userID, s.questionID) {
				s.sendError(speech.ErrInvalidRequest)
				return
			}
			s.state = asrStateReady
			if err := s.sendReady(); err != nil {
				return
			}
		case event := <-s.providerEvents:
			if s.handleProviderEvent(event) {
				return
			}
		case result := <-s.finishResults:
			if result.err != nil {
				if !result.fallback && shouldFallbackASR(result.err) {
					if err := s.enterFallbackMode(result.err); err != nil {
						return
					}
					if err := s.startFallback(); err != nil {
						s.sendError(publicASRError(err))
						return
					}
					continue
				}
				s.sendError(publicASRError(result.err))
				return
			}
			if !s.questions.Match(s.userID, s.questionID) {
				s.sendError(speech.ErrInvalidRequest)
				return
			}
			if err := s.sendJSON(asrServerMessage{
				Type: "asr.final", QuestionID: s.questionID, Text: result.event.Text,
				Degraded: result.event.Degraded || result.fallback || s.degraded, ProviderRequestID: result.event.ProviderRequestID,
			}); err != nil {
				return
			}
			s.logEvent(speech.Event{
				Name: speech.EventASRCompleted, Model: s.modelForResult(result.fallback),
				ProviderRequestID: result.event.ProviderRequestID,
				TextChars:         utf8.RuneCountInString(result.event.Text),
				Degraded:          result.event.Degraded || result.fallback || s.degraded,
			})
			s.terminalLogged = true
			s.writeClose(websocket.CloseNormalClosure, "")
			return
		case <-s.readErrors:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *asrWebSocketSession) readPump() {
	defer s.wg.Done()
	for {
		messageType, data, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case s.readErrors <- err:
			default:
			}
			return
		}
		frame := asrClientFrame{messageType: messageType, data: data}
		select {
		case s.frames <- frame:
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *asrWebSocketSession) handleClientFrame(frame asrClientFrame) bool {
	if frame.messageType == websocket.BinaryMessage {
		return s.handleAudio(frame.data)
	}
	if frame.messageType != websocket.TextMessage {
		return s.protocolViolation("仅支持 ASR JSON 控制消息和 PCM 二进制帧")
	}
	control, err := decodeASRControl(frame.data)
	if err != nil {
		return s.protocolViolation("ASR 控制消息格式无效")
	}

	switch control.Type {
	case "asr.start":
		if s.state != asrStateConnected {
			return s.protocolViolation("ASR 会话只能启动一次")
		}
		if err := s.validateStart(control); err != nil {
			return s.protocolViolation("ASR 启动参数或问题 ID 无效")
		}
		s.questionID = control.QuestionID
		s.startedAt = time.Now()
		s.state = asrStateStarting
		s.logEvent(speech.Event{Name: speech.EventASRStarted, Model: s.realtimeModel})
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			err := s.reservation.Start(s.ctx, speech.ASRConfig{
				QuestionID: control.QuestionID,
				SampleRate: control.SampleRate,
				Channels:   control.Channels,
				Format:     control.Format,
			}, s.publishProviderEvent)
			select {
			case s.startResults <- asrStartResult{err: err}:
			case <-s.ctx.Done():
			}
		}()
		return false
	case "asr.stop":
		if control.hasStartFields() || (s.state != asrStateReady && s.state != asrStateStreaming) {
			return s.protocolViolation("当前 ASR 状态不能停止")
		}
		return s.beginFinish()
	case "asr.cancel":
		if control.hasStartFields() || s.state == asrStateConnected {
			return s.protocolViolation("当前 ASR 状态不能取消")
		}
		return true
	default:
		return s.protocolViolation("未知 ASR 控制消息")
	}
}

func (s *asrWebSocketSession) handleAudio(pcm []byte) bool {
	if s.state != asrStateReady && s.state != asrStateStreaming {
		return s.protocolViolation("ASR 尚未就绪或已经停止")
	}
	if !s.questions.Match(s.userID, s.questionID) {
		s.sendError(speech.ErrInvalidRequest)
		return true
	}
	if len(pcm) == 0 || len(pcm) > maxASRFrameBytes || len(pcm)%2 != 0 {
		return s.protocolViolation("PCM 帧大小或格式无效")
	}
	remaining := s.maxAudioBytes - s.buffer.Len()
	if remaining <= 0 {
		return s.beginFinish()
	}
	if len(pcm) > remaining {
		pcm = pcm[:remaining]
	}
	full, err := s.buffer.Append(pcm)
	if err != nil {
		s.sendError(publicASRError(err))
		return true
	}
	if !s.degraded {
		if err := s.reservation.WriteAudio(s.ctx, pcm); err != nil {
			if !shouldFallbackASR(err) {
				s.sendError(publicASRError(err))
				return true
			}
			if err := s.enterFallbackMode(err); err != nil {
				return true
			}
		}
	}
	s.state = asrStateStreaming
	if full {
		return s.beginFinish()
	}
	return false
}

func (s *asrWebSocketSession) beginFinish() bool {
	if s.state == asrStateStopping {
		return s.protocolViolation("ASR 已经在生成最终结果")
	}
	if s.buffer.Len() < minASRAudioBytes {
		s.sendError(speech.ErrEmptyAudio)
		return true
	}
	s.state = asrStateStopping
	if s.degraded {
		if err := s.startFallback(); err != nil {
			s.sendError(publicASRError(err))
			return true
		}
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		event, err := s.reservation.Finish(s.ctx)
		select {
		case s.finishResults <- asrFinishResult{event: event, err: err, fallback: false}:
		case <-s.ctx.Done():
		}
	}()
	return false
}

func (s *asrWebSocketSession) handleProviderEvent(event speech.TranscriptEvent) bool {
	if !s.questions.Match(s.userID, s.questionID) {
		return false
	}
	switch event.Kind {
	case "partial":
		if s.degraded {
			return false
		}
		if event.Seq == 0 || event.Seq <= s.lastSeq || strings.TrimSpace(event.Text) == "" {
			return false
		}
		if s.state != asrStateReady && s.state != asrStateStreaming && s.state != asrStateStopping {
			return false
		}
		s.lastSeq = event.Seq
		if err := s.sendJSON(asrServerMessage{
			Type: "asr.partial", QuestionID: s.questionID, Seq: event.Seq, Text: event.Text,
		}); err != nil {
			return true
		}
		if !s.firstPartialLogged {
			s.firstPartialLogged = true
			s.logEvent(speech.Event{
				Name: speech.EventASRFirstPartial, Model: s.realtimeModel,
				FirstResultMS:     time.Since(s.startedAt).Milliseconds(),
				TextChars:         utf8.RuneCountInString(event.Text),
				ProviderRequestID: event.ProviderRequestID,
			})
		}
		return false
	case "warning":
		if s.state != asrStateReady && s.state != asrStateStreaming && s.state != asrStateStopping {
			return false
		}
		return s.enterFallbackMode(speech.ErrASRFinalFailed) != nil
	default:
		return false
	}
}

func (s *asrWebSocketSession) sendReady() error {
	return s.sendJSON(asrServerMessage{
		Type: "asr.ready", QuestionID: s.questionID, RequestID: s.requestID,
	})
}

func (s *asrWebSocketSession) enterFallbackMode(cause error) error {
	if !s.degraded {
		s.degraded = true
		_ = s.reservation.DegradeRealtime()
		s.logEvent(speech.Event{
			Name: speech.EventASRRealtimeDegraded, Model: s.realtimeModel,
			Degraded: true, ErrorCode: publicASRError(cause).Code,
		})
	}
	if s.warningSent {
		return nil
	}
	s.warningSent = true
	return s.sendJSON(asrServerMessage{
		Type: "asr.warning", QuestionID: s.questionID, Code: asrDegradedCode,
		Message: asrDegradedMessage,
	})
}

func (s *asrWebSocketSession) startFallback() error {
	if s.fallbackStarted {
		return speech.ErrInvalidRequest
	}
	s.fallbackStarted = true
	pcm := s.buffer.Bytes()
	s.logEvent(speech.Event{
		Name: speech.EventASRFallbackStarted, Model: s.fallbackModel, Degraded: true,
	})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		event, err := s.reservation.TranscribeFallback(s.ctx, pcm)
		select {
		case s.finishResults <- asrFinishResult{event: event, err: err, fallback: true}:
		case <-s.ctx.Done():
		}
	}()
	return nil
}

func (s *asrWebSocketSession) publishProviderEvent(event speech.TranscriptEvent) {
	select {
	case s.providerEvents <- event:
	case <-s.ctx.Done():
	default:
		// Partial updates are disposable under pressure. Final is returned by
		// Finish, so dropping an intermediate preview cannot lose final text.
	}
}

func (s *asrWebSocketSession) validateStart(control asrClientControl) error {
	if len(control.QuestionID) > 64 || control.Format != speech.InputFormatPCMS16LE || control.SampleRate != speech.InputSampleRate || control.Channels != 1 {
		return speech.ErrInvalidRequest
	}
	if _, err := uuid.Parse(control.QuestionID); err != nil {
		return speech.ErrInvalidRequest
	}
	if !s.questions.Match(s.userID, control.QuestionID) {
		return speech.ErrInvalidRequest
	}
	return nil
}

func (s *asrWebSocketSession) protocolViolation(message string) bool {
	s.violations++
	_ = s.sendJSON(asrServerMessage{
		Type: "asr.error", QuestionID: s.questionID, Code: speech.CodeInvalidRequest,
		Message: message,
	})
	terminal := s.violations >= maxASRViolations
	if terminal && !s.terminalLogged {
		s.logEvent(speech.Event{Name: speech.EventASRFailed, ErrorCode: speech.CodeInvalidRequest})
		s.terminalLogged = true
	}
	return terminal
}

func (s *asrWebSocketSession) sendError(publicError *speech.Error) {
	if publicError == nil {
		publicError = speech.ErrASRFinalFailed
	}
	_ = s.sendJSON(asrServerMessage{
		Type: "asr.error", QuestionID: s.questionID, Code: publicError.Code,
		Message: publicError.Message, Retryable: publicError.Retryable,
	})
	if !s.terminalLogged {
		s.logEvent(speech.Event{
			Name: speech.EventASRFailed, Model: s.modelForResult(s.fallbackStarted),
			Degraded: s.degraded, ErrorCode: publicError.Code,
		})
		s.terminalLogged = true
	}
}

func (s *asrWebSocketSession) logEvent(event speech.Event) {
	if s.logger == nil {
		return
	}
	event.RequestID = s.requestID
	event.QuestionID = s.questionID
	event.UserID = s.userID
	if event.Provider == "" {
		event.Provider = s.provider
	}
	if !s.startedAt.IsZero() && event.DurationMS == 0 {
		event.DurationMS = time.Since(s.startedAt).Milliseconds()
	}
	event.AudioBytes = s.buffer.Len()
	s.logger.LogSpeechEvent(s.ctx, event)
}

func (s *asrWebSocketSession) modelForResult(fallback bool) string {
	if fallback {
		return s.fallbackModel
	}
	return s.realtimeModel
}

func (s *asrWebSocketSession) sendJSON(message asrServerMessage) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.conn.WriteJSON(message)
}

func (s *asrWebSocketSession) writeClose(code int, message string) {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(time.Second))
}

func decodeASRControl(data []byte) (asrClientControl, error) {
	if len(data) == 0 || len(data) > maxASRControlBytes {
		return asrClientControl{}, speech.ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var control asrClientControl
	if err := decoder.Decode(&control); err != nil {
		return asrClientControl{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return asrClientControl{}, speech.ErrInvalidRequest
	}
	return control, nil
}

func (c asrClientControl) hasStartFields() bool {
	return c.QuestionID != "" || c.Format != "" || c.SampleRate != 0 || c.Channels != 0
}

func publicASRError(err error) *speech.Error {
	switch {
	case errors.Is(err, speech.ErrInvalidRequest):
		return speech.ErrInvalidRequest
	case errors.Is(err, speech.ErrEmptyAudio):
		return speech.ErrEmptyAudio
	case errors.Is(err, speech.ErrClientCancelled):
		return speech.ErrClientCancelled
	case errors.Is(err, speech.ErrASRFinalFailed):
		return speech.ErrASRFinalFailed
	case errors.Is(err, speech.ErrUpstreamTimeout):
		return speech.ErrUpstreamTimeout
	case errors.Is(err, speech.ErrUpstreamProtocol):
		return speech.ErrUpstreamProtocol
	default:
		return speech.ErrUpstreamUnavailable
	}
}

func shouldFallbackASR(err error) bool {
	return errors.Is(err, speech.ErrUpstreamTimeout) ||
		errors.Is(err, speech.ErrUpstreamUnavailable) ||
		errors.Is(err, speech.ErrUpstreamProtocol) ||
		errors.Is(err, speech.ErrASRFinalFailed)
}
