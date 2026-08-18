package dashscope

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"interview-agent/internal/speech"
)

const (
	maxRealtimeServerMessage = 1024 * 1024
	maxRealtimeAudioFrame    = 64 * 1024
)

type RealtimeASRConfig struct {
	APIKey         string
	URL            string
	Model          string
	ConnectTimeout time.Duration
	WriteTimeout   time.Duration
	QueueSize      int
	Dialer         *websocket.Dialer
}

// RealtimeASRClient provides the streaming half of ASRClient using
// Qwen-ASR-Realtime's native WebSocket protocol.
type RealtimeASRClient struct {
	apiKey         string
	endpoint       *url.URL
	model          string
	connectTimeout time.Duration
	writeTimeout   time.Duration
	queueSize      int
	dialer         *websocket.Dialer
}

func NewRealtimeASRClient(cfg RealtimeASRConfig) (*RealtimeASRClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("dashscope ASR: API key is required")
	}
	endpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("dashscope ASR: invalid realtime URL")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("dashscope ASR: model is required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 32
	}
	dialer := websocket.DefaultDialer
	if cfg.Dialer != nil {
		dialer = cfg.Dialer
	}
	dialerCopy := *dialer
	dialerCopy.HandshakeTimeout = cfg.ConnectTimeout
	return &RealtimeASRClient{
		apiKey:         strings.TrimSpace(cfg.APIKey),
		endpoint:       endpoint,
		model:          strings.TrimSpace(cfg.Model),
		connectTimeout: cfg.ConnectTimeout,
		writeTimeout:   cfg.WriteTimeout,
		queueSize:      cfg.QueueSize,
		dialer:         &dialerCopy,
	}, nil
}

func (c *RealtimeASRClient) Start(
	ctx context.Context,
	cfg speech.ASRConfig,
	onEvent func(speech.TranscriptEvent),
) (speech.ASRStream, error) {
	if cfg.QuestionID == "" || cfg.Format != speech.InputFormatPCMS16LE || cfg.SampleRate != speech.InputSampleRate || cfg.Channels != 1 || onEvent == nil {
		return nil, speech.ErrInvalidRequest
	}

	connectCtx, cancel := context.WithTimeout(ctx, c.connectTimeout)
	defer cancel()
	endpoint := *c.endpoint
	query := endpoint.Query()
	query.Set("model", c.model)
	endpoint.RawQuery = query.Encode()
	headers := http.Header{
		"Authorization": []string{"Bearer " + c.apiKey},
		"User-Agent":    []string{"InterviewAgent-Go/1.0"},
	}
	conn, response, err := c.dialer.DialContext(connectCtx, endpoint.String(), headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, mapRealtimeConnectError(connectCtx, err)
	}
	stopContextClose := context.AfterFunc(connectCtx, func() { _ = conn.Close() })
	defer stopContextClose()
	keepConnection := false
	defer func() {
		if !keepConnection {
			_ = conn.Close()
		}
	}()
	conn.SetReadLimit(maxRealtimeServerMessage)
	_ = conn.SetReadDeadline(time.Now().Add(c.connectTimeout))

	created, err := readRealtimeServerEvent(conn)
	if err != nil {
		return nil, mapRealtimeConnectError(connectCtx, err)
	}
	if created.Type == "error" {
		return nil, safeRealtimeProviderError(created.Error.Code)
	}
	if created.Type != "session.created" || strings.TrimSpace(created.Session.ID) == "" {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("missing session.created"))
	}

	_ = conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	update := map[string]any{
		"event_id": "event_" + uuid.NewString(),
		"type":     "session.update",
		"session": map[string]any{
			"input_audio_format":        "pcm",
			"sample_rate":               speech.InputSampleRate,
			"input_audio_transcription": map[string]any{},
			"turn_detection":            nil,
		},
	}
	if err := conn.WriteJSON(update); err != nil {
		return nil, mapRealtimeConnectError(connectCtx, err)
	}
	updated, err := readRealtimeServerEvent(conn)
	if err != nil {
		return nil, mapRealtimeConnectError(connectCtx, err)
	}
	if updated.Type == "error" {
		return nil, safeRealtimeProviderError(updated.Error.Code)
	}
	if updated.Type != "session.updated" {
		return nil, speech.WithCause(speech.ErrUpstreamProtocol, errors.New("missing session.updated"))
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	streamCtx, streamCancel := context.WithCancel(ctx)
	stream := &realtimeASRStream{
		conn:         conn,
		ctx:          streamCtx,
		cancel:       streamCancel,
		writeTimeout: c.writeTimeout,
		writeCh:      make(chan realtimeWriteCommand, c.queueSize),
		onEvent:      onEvent,
		requestID:    created.Session.ID,
		failureCh:    make(chan struct{}),
		finalCh:      make(chan speech.TranscriptEvent, 1),
		finishedCh:   make(chan struct{}),
	}
	stream.wg.Add(2)
	go stream.writeLoop()
	go stream.readLoop()
	keepConnection = true
	return stream, nil
}

type realtimeASRStream struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	writeTimeout time.Duration
	writeCh      chan realtimeWriteCommand
	onEvent      func(speech.TranscriptEvent)
	requestID    string
	seq          atomic.Uint64

	stateMu   sync.Mutex
	finishing bool
	closed    bool

	failureOnce  sync.Once
	failureCh    chan struct{}
	failureErr   error
	finalOnce    sync.Once
	finalCh      chan speech.TranscriptEvent
	finishedOnce sync.Once
	finishedCh   chan struct{}

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func (s *realtimeASRStream) WriteAudio(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 || len(pcm) > maxRealtimeAudioFrame || len(pcm)%2 != 0 {
		return speech.ErrInvalidRequest
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return speech.ErrClientCancelled
	}
	if s.finishing {
		s.stateMu.Unlock()
		return speech.ErrInvalidRequest
	}
	s.stateMu.Unlock()
	if err := s.failure(); err != nil {
		return err
	}
	copyPCM := append([]byte(nil), pcm...)
	command := realtimeWriteCommand{audio: copyPCM}
	select {
	case s.writeCh <- command:
		return nil
	case <-ctx.Done():
		return speech.WithCause(speech.ErrClientCancelled, ctx.Err())
	case <-s.ctx.Done():
		return speech.ErrClientCancelled
	default:
		return speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("ASR audio queue full"))
	}
}

func (s *realtimeASRStream) Finish(ctx context.Context) (speech.TranscriptEvent, error) {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return speech.TranscriptEvent{}, speech.ErrClientCancelled
	}
	if s.finishing {
		s.stateMu.Unlock()
		return speech.TranscriptEvent{}, speech.ErrInvalidRequest
	}
	s.finishing = true
	s.stateMu.Unlock()

	writeResult := make(chan error, 1)
	command := realtimeWriteCommand{finish: true, result: writeResult}
	select {
	case s.writeCh <- command:
	case <-ctx.Done():
		return speech.TranscriptEvent{}, mapRealtimeContextError(ctx.Err())
	case <-s.failureCh:
		return speech.TranscriptEvent{}, s.failure()
	case <-s.ctx.Done():
		return speech.TranscriptEvent{}, speech.ErrClientCancelled
	}
	select {
	case err := <-writeResult:
		if err != nil {
			return speech.TranscriptEvent{}, err
		}
	case <-ctx.Done():
		return speech.TranscriptEvent{}, mapRealtimeContextError(ctx.Err())
	case <-s.failureCh:
		return speech.TranscriptEvent{}, s.failure()
	case <-s.ctx.Done():
		return speech.TranscriptEvent{}, speech.ErrClientCancelled
	}

	var final *speech.TranscriptEvent
	for {
		select {
		case event := <-s.finalCh:
			final = &event
		case <-s.finishedCh:
			if final == nil {
				select {
				case event := <-s.finalCh:
					final = &event
				default:
				}
			}
			if final == nil || strings.TrimSpace(final.Text) == "" {
				return speech.TranscriptEvent{}, speech.ErrEmptyAudio
			}
			return *final, nil
		case <-ctx.Done():
			return speech.TranscriptEvent{}, mapRealtimeContextError(ctx.Err())
		case <-s.failureCh:
			return speech.TranscriptEvent{}, s.failure()
		case <-s.ctx.Done():
			return speech.TranscriptEvent{}, speech.ErrClientCancelled
		}
	}
}

func (s *realtimeASRStream) Close() error {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closed = true
		s.stateMu.Unlock()
		s.cancel()
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = s.conn.Close()
		s.wg.Wait()
	})
	return nil
}

func (s *realtimeASRStream) writeLoop() {
	defer s.wg.Done()
	for {
		select {
		case command := <-s.writeCh:
			var err error
			if command.finish {
				err = s.writeJSON(map[string]any{"event_id": "event_" + uuid.NewString(), "type": "input_audio_buffer.commit"})
				if err == nil {
					err = s.writeJSON(map[string]any{"event_id": "event_" + uuid.NewString(), "type": "session.finish"})
				}
			} else {
				err = s.writeJSON(map[string]any{
					"event_id": "event_" + uuid.NewString(),
					"type":     "input_audio_buffer.append",
					"audio":    base64.StdEncoding.EncodeToString(command.audio),
				})
			}
			if command.result != nil {
				command.result <- err
			}
			if err != nil {
				s.fail(speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("ASR upstream write failed")))
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *realtimeASRStream) readLoop() {
	defer s.wg.Done()
	for {
		event, err := readRealtimeServerEvent(s.conn)
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			case <-s.finishedCh:
				return
			default:
				s.fail(speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("ASR upstream read failed")))
				return
			}
		}
		switch event.Type {
		case "conversation.item.input_audio_transcription.text":
			text := event.Text + event.Stash
			if text != "" {
				s.onEvent(speech.TranscriptEvent{
					Kind: "partial", Seq: s.seq.Add(1), Text: text, ProviderRequestID: s.requestID,
				})
			}
		case "conversation.item.input_audio_transcription.completed":
			if strings.TrimSpace(event.Transcript) != "" {
				s.finalOnce.Do(func() {
					s.finalCh <- speech.TranscriptEvent{
						Kind: "final", Text: event.Transcript, ProviderRequestID: s.requestID,
					}
				})
			}
		case "session.finished":
			s.finishedOnce.Do(func() { close(s.finishedCh) })
			return
		case "conversation.item.input_audio_transcription.failed", "error":
			s.fail(safeRealtimeProviderError(event.Error.Code))
			return
		}
	}
}

func (s *realtimeASRStream) writeJSON(value any) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	return s.conn.WriteJSON(value)
}

func (s *realtimeASRStream) fail(err error) {
	s.failureOnce.Do(func() {
		s.failureErr = err
		close(s.failureCh)
		_ = s.conn.Close()
	})
}

func (s *realtimeASRStream) failure() error {
	select {
	case <-s.failureCh:
		if s.failureErr != nil {
			return s.failureErr
		}
		return speech.ErrUpstreamUnavailable
	default:
		return nil
	}
}

func readRealtimeServerEvent(conn *websocket.Conn) (realtimeServerEvent, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return realtimeServerEvent{}, err
	}
	if len(data) > maxRealtimeServerMessage {
		return realtimeServerEvent{}, errors.New("ASR provider event too large")
	}
	var event realtimeServerEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return realtimeServerEvent{}, errors.New("invalid ASR provider event")
	}
	return event, nil
}

func safeRealtimeProviderError(code string) error {
	code = strings.TrimSpace(code)
	if len(code) > 64 {
		code = code[:64]
	}
	return speech.WithCause(speech.ErrUpstreamProtocol, fmt.Errorf("ASR provider error code=%q", code))
}

func mapRealtimeConnectError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return speech.WithCause(speech.ErrClientCancelled, errors.New("ASR connect cancelled"))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return speech.WithCause(speech.ErrUpstreamTimeout, errors.New("ASR connect timed out"))
	}
	var netError interface{ Timeout() bool }
	if errors.As(err, &netError) && netError.Timeout() {
		return speech.WithCause(speech.ErrUpstreamTimeout, errors.New("ASR connect timed out"))
	}
	return speech.WithCause(speech.ErrUpstreamUnavailable, errors.New("ASR connect failed"))
}

func mapRealtimeContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return speech.WithCause(speech.ErrUpstreamTimeout, errors.New("ASR final timed out"))
	}
	return speech.WithCause(speech.ErrClientCancelled, errors.New("ASR request cancelled"))
}
