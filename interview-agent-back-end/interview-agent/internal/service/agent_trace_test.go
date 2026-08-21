package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

func TestAgentTraceBindsTrainingAttempt(t *testing.T) {
	ctx := context.Background()
	traceService := NewAgentTraceService(memory.NewMemoryStore())
	if _, err := traceService.Create(ctx, "attempt-session", "student-001", "improve communication"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := traceService.BindTrainingAttempt(ctx, "attempt-session", "attempt-primary-001"); err != nil {
		t.Fatalf("BindTrainingAttempt() error = %v", err)
	}
	trace, err := traceService.GetBySessionID(ctx, "attempt-session")
	if err != nil {
		t.Fatalf("GetBySessionID() error = %v", err)
	}
	if trace.TrainingAttemptID != "attempt-primary-001" {
		t.Fatalf("training attempt id = %q", trace.TrainingAttemptID)
	}
}

func TestAgentTraceRecordsAbilityChangeFromGrowthFlow(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemoryStore()
	if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID: "student-growth-trace",
		AbilityScores: map[string]float64{
			imodel.AbilityCommunication: 0.55,
		},
	}); err != nil {
		t.Fatalf("SaveAbilityProfile() error = %v", err)
	}
	traceService := NewAgentTraceService(store)
	if _, err := traceService.Create(ctx, "growth-trace-session", "student-growth-trace", "improve communication"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	before := map[string]float64{imodel.AbilityCommunication: 0.55}
	attempt := &imodel.TrainingAttempt{
		ID: "attempt-growth-001", StudentID: "student-growth-trace",
		EvaluationResult: &imodel.EvaluationResult{Score: 75, EvaluatedAt: time.Now()},
	}
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)
	result, err := growthService.UpdateAbilityProfile(ctx, "student-growth-trace", GrowthRecordInput{
		SessionID: "growth-trace-session", LearningGoal: "提升表达能力", OverallScore: 75,
		AbilityScores:    map[string]float64{imodel.AbilityCommunication: 0.75},
		TrainingAttempts: []*imodel.TrainingAttempt{attempt}, TrainingTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateAbilityProfile() error = %v", err)
	}
	if err := traceService.SaveAbilityChanges(ctx, "growth-trace-session", before, result.Profile.AbilityScores); err != nil {
		t.Fatalf("SaveAbilityChanges() error = %v", err)
	}
	trace, err := traceService.GetBySessionID(ctx, "growth-trace-session")
	if err != nil {
		t.Fatalf("GetBySessionID() error = %v", err)
	}
	if trace.AbilityBefore[imodel.AbilityCommunication] != 0.55 || trace.AbilityAfter[imodel.AbilityCommunication] != 0.65 {
		t.Fatalf("ability change = %.2f -> %.2f, want 0.55 -> 0.65", trace.AbilityBefore[imodel.AbilityCommunication], trace.AbilityAfter[imodel.AbilityCommunication])
	}
}

func TestAgentTraceDoesNotPersistSensitivePayloads(t *testing.T) {
	ctx := context.Background()
	traceService := NewAgentTraceService(memory.NewMemoryStore())
	if _, err := traceService.Create(ctx, "privacy-session", "student-001", "improve communication"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := traceService.RecordMemorySummary(ctx, "privacy-session", "contact=13800138000 email=student@example.com"); err != nil {
		t.Fatalf("RecordMemorySummary() error = %v", err)
	}
	trace, err := traceService.GetBySessionID(ctx, "privacy-session")
	if err != nil {
		t.Fatalf("GetBySessionID() error = %v", err)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	serialized := string(data)
	for _, forbidden := range []string{"13800138000", "student@example.com", `"prompt"`, `"answer"`, `"token"`} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("trace contains forbidden sensitive payload %q: %s", forbidden, serialized)
		}
	}
	if !strings.Contains(serialized, "[redacted-phone]") || !strings.Contains(serialized, "[redacted-email]") {
		t.Fatalf("sensitive summaries were not redacted: %s", serialized)
	}
}
