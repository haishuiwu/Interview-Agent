package handler

import "sync"

// activeQuestionRegistry binds the control WebSocket's current question to the
// authenticated user. ASR connections consult it so stale or cross-session
// question IDs cannot publish transcripts into a newer interview turn.
type activeQuestionRegistry struct {
	mu        sync.RWMutex
	questions map[string]string
}

func newActiveQuestionRegistry() *activeQuestionRegistry {
	return &activeQuestionRegistry{questions: make(map[string]string)}
}

func (r *activeQuestionRegistry) Set(userID, questionID string) {
	if r == nil || userID == "" || questionID == "" {
		return
	}
	r.mu.Lock()
	r.questions[userID] = questionID
	r.mu.Unlock()
}

func (r *activeQuestionRegistry) Match(userID, questionID string) bool {
	if r == nil || userID == "" || questionID == "" {
		return false
	}
	r.mu.RLock()
	current := r.questions[userID]
	r.mu.RUnlock()
	return current == questionID
}

func (r *activeQuestionRegistry) ClearIf(userID, questionID string) {
	if r == nil || userID == "" || questionID == "" {
		return
	}
	r.mu.Lock()
	if r.questions[userID] == questionID {
		delete(r.questions, userID)
	}
	r.mu.Unlock()
}
