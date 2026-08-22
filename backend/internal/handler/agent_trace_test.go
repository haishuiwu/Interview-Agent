package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"interview-agent/internal/memory"
	growthservice "interview-agent/internal/service"
)

func TestGetAgentTraceBySessionID(t *testing.T) {
	traceService := growthservice.NewAgentTraceService(memory.NewMemoryStore())
	ctx := context.Background()
	if _, err := traceService.Create(ctx, "api-trace-session", "default_user", "improve communication"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := traceService.UpdateSkill(ctx, "api-trace-session", "communication-training", "learning goal targets communication ability"); err != nil {
		t.Fatalf("UpdateSkill() error = %v", err)
	}

	server := NewServer(&ServerConfig{AgentTraceService: traceService})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/trace/api-trace-session", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	trace := &agentTraceResponse{}
	if err := json.NewDecoder(recorder.Body).Decode(trace); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if trace.SessionID != "api-trace-session" || trace.Skill != "communication-training" {
		t.Fatalf("trace response = %+v", trace)
	}
}
