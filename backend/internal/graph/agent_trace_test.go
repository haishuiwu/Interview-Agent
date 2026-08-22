package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"interview-agent/internal/memory"
	"interview-agent/internal/service"
)

func TestAgentTraceRecognizesCommunicationGoalAndSkill(t *testing.T) {
	ctx := context.Background()
	traceService := service.NewAgentTraceService(memory.NewMemoryStore())
	rawGoal := "我想提升表达能力，联系电话 13800138000，完整背景不应写入 Trace"
	trace, err := traceService.Create(ctx, "trace-communication", "student-001", traceIntentForGoal(rawGoal))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	skillName, reason := traceSkillDecision(rawGoal)
	if err := traceService.UpdateSkill(ctx, trace.SessionID, skillName, reason); err != nil {
		t.Fatalf("UpdateSkill() error = %v", err)
	}

	trace, err = traceService.GetBySessionID(ctx, trace.SessionID)
	if err != nil {
		t.Fatalf("GetBySessionID() error = %v", err)
	}
	if trace.Intent != "improve communication" {
		t.Fatalf("intent = %q, want improve communication", trace.Intent)
	}
	if trace.SelectedSkill != "communication-training" {
		t.Fatalf("selected skill = %q, want communication-training", trace.SelectedSkill)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), rawGoal) || strings.Contains(string(data), "13800138000") {
		t.Fatalf("trace persisted raw goal or sensitive value: %s", data)
	}
}
