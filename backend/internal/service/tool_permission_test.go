package service

import (
	"context"
	"testing"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

func TestTrustedEvaluationPathStillUpdatesAbilityProfile(t *testing.T) {
	store := memory.NewMemoryStore()
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)

	result, err := growthService.UpdateAbilityProfile(context.Background(), "student-evaluated", GrowthRecordInput{
		SessionID:     "evaluated-session",
		LearningGoal:  "提升逻辑思考",
		OverallScore:  90,
		AbilityScores: map[string]float64{imodel.AbilityLogicalThinking: 90},
		Summary:       "由正常训练评价生成",
	})
	if err != nil {
		t.Fatalf("trusted UpdateAbilityProfile() error = %v", err)
	}
	if got := result.Profile.AbilityScores[imodel.AbilityLogicalThinking]; got != 0.9 {
		t.Fatalf("ability score = %v, want 0.9", got)
	}
	if result.GrowthRecord == nil || result.GrowthRecord.SessionID != "evaluated-session" {
		t.Fatalf("growth record = %#v", result.GrowthRecord)
	}

	stored, err := store.LoadAbilityProfile(context.Background(), "student-evaluated")
	if err != nil || stored == nil || stored.AbilityScores[imodel.AbilityLogicalThinking] != 0.9 {
		t.Fatalf("stored profile = %#v, error = %v", stored, err)
	}
}
