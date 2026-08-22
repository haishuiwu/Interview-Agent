package handler

import (
	"context"

	"interview-agent/internal/speech"
)

func (s *Server) logSpeechEvent(ctx context.Context, event speech.Event) {
	if s == nil || s.cfg == nil || s.cfg.SpeechLogger == nil {
		return
	}
	s.cfg.SpeechLogger.LogSpeechEvent(ctx, event)
}
