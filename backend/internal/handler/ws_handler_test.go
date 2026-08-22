package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"interview-agent/internal/graph"
)

func newWSSessionTestConnection(t *testing.T) (*WSSession, *websocket.Conn) {
	t.Helper()

	serverConnCh := make(chan *websocket.Conn, 1)
	releaseServer := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade test websocket: %v", err)
			return
		}
		serverConnCh <- conn
		<-releaseServer
	}))

	clientConn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"),
		nil,
	)
	if err != nil {
		close(releaseServer)
		server.Close()
		t.Fatalf("dial test websocket: %v", err)
	}

	serverConn := <-serverConnCh
	ws := NewWSSession(serverConn, &ServerConfig{}, "test-user")

	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		close(releaseServer)
		server.Close()
	})

	return ws, clientConn
}

func beginTestTraining(t *testing.T, ws *WSSession) (context.Context, chan string, uint64) {
	t.Helper()

	ctx, answerCh, generation, ok := ws.beginTraining()
	if !ok {
		t.Fatal("beginTraining unexpectedly rejected a new training session")
	}
	return ctx, answerCh, generation
}

func waitUntilAwaitingAnswer(t *testing.T, ws *WSSession) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ws.trainingMu.Lock()
		awaiting := ws.awaitingAnswer
		ws.trainingMu.Unlock()
		if awaiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("session did not enter the awaiting-answer state")
}

func TestHandleQuitTrainingIsIdempotent(t *testing.T) {
	ws, _ := newWSSessionTestConnection(t)
	beginTestTraining(t, ws)

	ws.handleQuitTraining()
	ws.handleQuitTraining()
}

func TestHandleAnswerAfterQuitDoesNotPanic(t *testing.T) {
	ws, _ := newWSSessionTestConnection(t)
	ctx, answerCh, generation := beginTestTraining(t, ws)

	result := make(chan error, 1)
	go func() {
		_, err := ws.waitForAnswer(ctx, generation, answerCh)
		result <- err
	}()
	waitUntilAwaitingAnswer(t, ws)

	ws.handleQuitTraining()
	ws.handleAnswer("late answer")

	select {
	case err := <-result:
		if !errors.Is(err, graph.ErrUserQuit) {
			t.Fatalf("waitForAnswer error = %v, want ErrUserQuit", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForAnswer did not exit after quit")
	}
}

func TestHandleAnswerDoesNotBlockWhenAnswerPending(t *testing.T) {
	ws, _ := newWSSessionTestConnection(t)
	ctx, answerCh, generation := beginTestTraining(t, ws)

	answerResult := make(chan string, 1)
	go func() {
		answer, _ := ws.waitForAnswer(ctx, generation, answerCh)
		answerResult <- answer
	}()
	waitUntilAwaitingAnswer(t, ws)
	ws.handleAnswer("first answer")

	done := make(chan struct{})
	go func() {
		ws.handleAnswer("duplicate answer")
		close(done)
	}()

	select {
	case <-done:
		// Expected: duplicate input must never block the WebSocket read loop.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleAnswer blocked while an answer was already pending")
	}

	select {
	case answer := <-answerResult:
		if answer != "first answer" {
			t.Fatalf("accepted answer = %q, want first answer", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("first answer was not delivered")
	}
}

func TestBeginTrainingRejectsDuplicateStart(t *testing.T) {
	ws, clientConn := newWSSessionTestConnection(t)
	beginTestTraining(t, ws)

	ws.handleStartTraining("another learning goal", "another student profile")

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var msg ServerMsg
	if err := clientConn.ReadJSON(&msg); err != nil {
		t.Fatalf("read duplicate-start response: %v", err)
	}
	if msg.Code != "TRAINING_ALREADY_RUNNING" {
		t.Fatalf("response code = %q, want TRAINING_ALREADY_RUNNING", msg.Code)
	}
}

func TestAdaptTrainingInput(t *testing.T) {
	tests := []struct {
		name        string
		msg         ClientMsg
		wantGoal    string
		wantProfile string
	}{
		{
			name: "canonical fields take precedence",
			msg: ClientMsg{
				LearningGoal:   "new goal",
				StudentProfile: "new profile",
				LegacyTrainingInputDTO: LegacyTrainingInputDTO{
					Assessment: "legacy assessment",
					Profile:    "legacy profile",
				},
			},
			wantGoal:    "new goal",
			wantProfile: "new profile",
		},
		{
			name: "transition fields are adapted",
			msg: ClientMsg{LegacyTrainingInputDTO: LegacyTrainingInputDTO{
				Assessment: "assessment",
				Profile:    "profile",
			}},
			wantGoal:    "assessment",
			wantProfile: "profile",
		},
		{
			name: "oldest fields are adapted",
			msg: ClientMsg{LegacyTrainingInputDTO: LegacyTrainingInputDTO{
				JD:     "legacy goal",
				Resume: "legacy student profile",
			}},
			wantGoal:    "legacy goal",
			wantProfile: "legacy student profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goal, profile := adaptTrainingInput(tt.msg)
			if goal != tt.wantGoal || profile != tt.wantProfile {
				t.Fatalf("adaptTrainingInput() = (%q, %q), want (%q, %q)", goal, profile, tt.wantGoal, tt.wantProfile)
			}
		})
	}
}

func TestWebSocketDisconnectCancelsAnswerWait(t *testing.T) {
	ws, clientConn := newWSSessionTestConnection(t)
	ctx, answerCh, generation := beginTestTraining(t, ws)

	result := make(chan error, 1)
	go func() {
		_, err := ws.waitForAnswer(ctx, generation, answerCh)
		result <- err
	}()
	waitUntilAwaitingAnswer(t, ws)

	runDone := make(chan struct{})
	go func() {
		ws.Run()
		close(runDone)
	}()
	if err := clientConn.Close(); err != nil {
		t.Fatalf("close websocket client: %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, graph.ErrUserQuit) {
			t.Fatalf("waitForAnswer error = %v, want ErrUserQuit", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForAnswer did not exit after websocket disconnect")
	}

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("websocket session did not exit after client disconnect")
	}
}

func TestHandleAnswerAndQuitConcurrently(t *testing.T) {
	ws, _ := newWSSessionTestConnection(t)

	for i := 0; i < 25; i++ {
		ctx, answerCh, generation := beginTestTraining(t, ws)
		result := make(chan error, 1)
		go func() {
			_, err := ws.waitForAnswer(ctx, generation, answerCh)
			result <- err
		}()
		waitUntilAwaitingAnswer(t, ws)

		var handlers sync.WaitGroup
		handlers.Add(2)
		go func() {
			defer handlers.Done()
			ws.handleAnswer("answer")
		}()
		go func() {
			defer handlers.Done()
			ws.handleQuitTraining()
		}()
		handlers.Wait()

		select {
		case err := <-result:
			if err != nil && !errors.Is(err, graph.ErrUserQuit) {
				t.Fatalf("iteration %d: unexpected waitForAnswer error: %v", i, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: waitForAnswer did not return", i)
		}

		ws.finishTraining(generation)
	}
}

func TestSendTrainingQuestionAddsSpeechFieldsWithoutChangingContent(t *testing.T) {
	ws, clientConn := newWSSessionTestConnection(t)
	ws.cfg.SpeechService = newFakeSpeechService(t, true)
	ws.cfg.speechQuestions = newActiveQuestionRegistry()
	_, _, generation := beginTestTraining(t, ws)
	content := "## **请介绍** Go 的 `GMP`。\n\n`[来源: LLM 出题]`"

	ws.sendTrainingQuestion(generation, 1, content)

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var msg ServerMsg
	if err := clientConn.ReadJSON(&msg); err != nil {
		t.Fatalf("read question: %v", err)
	}
	if msg.Content != content {
		t.Fatalf("content changed: %q", msg.Content)
	}
	if _, err := uuid.Parse(msg.QuestionID); err != nil {
		t.Fatalf("question_id = %q: %v", msg.QuestionID, err)
	}
	if msg.SpeechText != "请介绍 Go 的 GMP。" {
		t.Fatalf("speech_text = %q", msg.SpeechText)
	}
	if !ws.cfg.speechQuestions.Match(ws.userID, msg.QuestionID) {
		t.Fatal("current question was not published for the ASR endpoint")
	}
	ws.handleAnswer("answer")
	if ws.cfg.speechQuestions.Match(ws.userID, msg.QuestionID) {
		t.Fatal("answered question remained available to the ASR endpoint")
	}
}

func TestSendTrainingQuestionOmitsSpeechFieldsWhenDisabled(t *testing.T) {
	ws, clientConn := newWSSessionTestConnection(t)
	_, _, generation := beginTestTraining(t, ws)

	ws.sendTrainingQuestion(generation, 1, "text-only question")

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var msg ServerMsg
	if err := clientConn.ReadJSON(&msg); err != nil {
		t.Fatalf("read question: %v", err)
	}
	if msg.QuestionID != "" || msg.SpeechText != "" {
		t.Fatalf("disabled speech fields = question_id:%q speech_text:%q", msg.QuestionID, msg.SpeechText)
	}
}

func TestSendTrainingQuestionStillSendsTextWhenSpeechNormalizationFails(t *testing.T) {
	ws, clientConn := newWSSessionTestConnection(t)
	ws.cfg.SpeechService = newTTSTestService(t, true, &fakeSpeechSynthesizer{}, 20, time.Second, 5)
	_, _, generation := beginTestTraining(t, ws)
	content := "这是一个超过语音上限但必须显示的文字问题"

	ws.sendTrainingQuestion(generation, 1, content)

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var msg ServerMsg
	if err := clientConn.ReadJSON(&msg); err != nil {
		t.Fatalf("read question: %v", err)
	}
	if msg.Content != content || msg.QuestionID == "" || msg.SpeechText != "" {
		t.Fatalf("question after speech fallback = %+v", msg)
	}
}
