package service

import (
	"context"
	"testing"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

func TestGrowthRecordKeepsEvaluatedTrainingAttemptIDs(t *testing.T) {
	store := memory.NewMemoryStore()
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)
	attempt := &imodel.TrainingAttempt{
		ID:               "attempt-001",
		EvaluationResult: &imodel.EvaluationResult{Score: 80},
	}
	incompleteAttempt := &imodel.TrainingAttempt{ID: "attempt-incomplete"}

	result, err := growthService.UpdateAbilityProfile(context.Background(), "student-attempt", GrowthRecordInput{
		SessionID:        "session-attempt",
		TrainingAttempts: []*imodel.TrainingAttempt{attempt, incompleteAttempt, attempt},
		LearningGoal:     "提升逻辑思考",
		OverallScore:     80,
		AbilityScores:    map[string]float64{imodel.AbilityLogicalThinking: 80},
	})
	if err != nil {
		t.Fatalf("UpdateAbilityProfile() error = %v", err)
	}
	if len(result.Profile.GrowthHistory) != 1 {
		t.Fatalf("growth history = %#v", result.Profile.GrowthHistory)
	}
	ids := result.Profile.GrowthHistory[0].TrainingAttemptIDs
	if len(ids) != 1 || ids[0] != attempt.ID {
		t.Fatalf("training attempt ids = %#v", ids)
	}
}
