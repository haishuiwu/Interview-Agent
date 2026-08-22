package service

import (
	"context"
	"math"
	"testing"
	"time"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

func TestFirstTrainingCreatesAbilityProfile(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemoryStore()
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)
	trainedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	result, err := growthService.UpdateAbilityProfile(ctx, "student_001", GrowthRecordInput{
		SessionID:     "session-first",
		LearningGoal:  "提升表达能力",
		OverallScore:  62,
		AbilityScores: map[string]float64{imodel.AbilityCommunication: 62},
		Strengths:     []string{"能够说明主要观点"},
		Weaknesses:    []string{"缺少结构化表达"},
		Summary:       "首次训练显示表达结构需要加强。",
		TrainingTime:  trainedAt,
	})
	if err != nil {
		t.Fatalf("UpdateAbilityProfile() error = %v", err)
	}
	if result.Profile.StudentID != "student_001" {
		t.Fatalf("student_id = %q", result.Profile.StudentID)
	}
	assertAbilityScore(t, result.Profile.AbilityScores, imodel.AbilityCommunication, 0.62)
	if len(result.Profile.GrowthHistory) != 1 || result.Profile.GrowthHistory[0].SessionID != "session-first" {
		t.Fatalf("growth_history = %#v", result.Profile.GrowthHistory)
	}
	if !result.Profile.LastTrainingTime.Equal(trainedAt) {
		t.Fatalf("last_training_time = %s, want %s", result.Profile.LastTrainingTime, trainedAt)
	}
	stored, err := store.LoadAbilityProfile(ctx, "student_001")
	if err != nil || stored == nil {
		t.Fatalf("LoadAbilityProfile() profile=%v error=%v", stored, err)
	}
	assertAbilityScore(t, stored.AbilityScores, imodel.AbilityCommunication, 0.62)
}

func TestAbilityProfileChangePersistsGrowthRecord(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemoryStore()
	if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID: "student_002",
		AbilityScores: map[string]float64{
			imodel.AbilityCommunication:   0.55,
			imodel.AbilityLogicalThinking: 0.80,
		},
	}); err != nil {
		t.Fatalf("SaveAbilityProfile() error = %v", err)
	}
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)

	result, err := growthService.UpdateAbilityProfile(ctx, "student_002", GrowthRecordInput{
		SessionID:     "session-second",
		LearningGoal:  "表达能力进阶训练",
		OverallScore:  75,
		AbilityScores: map[string]float64{imodel.AbilityCommunication: 75},
		Weaknesses:    []string{"结论缺少验证"},
	})
	if err != nil {
		t.Fatalf("UpdateAbilityProfile() error = %v", err)
	}
	assertAbilityScore(t, result.Profile.AbilityScores, imodel.AbilityCommunication, 0.65)
	change := result.Profile.GrowthHistory[len(result.Profile.GrowthHistory)-1]
	assertAbilityScore(t, change.BeforeScores, imodel.AbilityCommunication, 0.55)
	assertAbilityScore(t, change.AfterScores, imodel.AbilityCommunication, 0.65)
	assertAbilityScore(t, change.ScoreChanges, imodel.AbilityCommunication, 0.10)
	if result.GrowthRecord == nil || result.GrowthRecord.SessionID != "session-second" {
		t.Fatalf("growth record = %#v", result.GrowthRecord)
	}
	legacyProfile, err := store.LoadProfile(ctx, "student_002")
	if err != nil || legacyProfile == nil || len(legacyProfile.InterviewHist) != 1 {
		t.Fatalf("saved growth history profile=%#v error=%v", legacyProfile, err)
	}
	if legacyProfile.InterviewHist[0].SessionID != "session-second" {
		t.Fatalf("saved session = %q", legacyProfile.InterviewHist[0].SessionID)
	}
	history, err := growthService.GetGrowthHistory(ctx, "student_002", imodel.AbilityCommunication, 5)
	if err != nil || len(history) != 1 || history[0].SessionID != "session-second" {
		t.Fatalf("communication growth history=%#v error=%v", history, err)
	}
}

func TestGenericRecommendationUsesWeakestStoredAbility(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemoryStore()
	_ = store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID: "student_003",
		AbilityScores: map[string]float64{
			imodel.AbilityCommunication:   0.55,
			imodel.AbilityLogicalThinking: 0.80,
		},
	})
	growthService := NewStudentGrowthDataService(store, nil, nil, nil)

	recommendation, err := growthService.RecommendTrainingTask(ctx, "student_003", "帮我训练一下", "", "")
	if err != nil {
		t.Fatalf("RecommendTrainingTask() error = %v", err)
	}
	if recommendation.SkillName != "communication-training" {
		t.Fatalf("skill = %q, want communication-training", recommendation.SkillName)
	}
	if recommendation.Ability != imodel.AbilityCommunication {
		t.Fatalf("ability = %q, want %s", recommendation.Ability, imodel.AbilityCommunication)
	}
	if got := RecommendStartingDifficulty(resultProfile(store, ctx, t, "student_003")); got != imodel.DifficultyMedium {
		t.Fatalf("starting difficulty = %s, want medium", got)
	}
}

func TestRecommendStartingDifficultyUsesAbilityProfile(t *testing.T) {
	profile := &imodel.StudentAbilityProfile{AbilityScores: map[string]float64{
		imodel.AbilityCommunication: 0.35,
	}}
	if got := RecommendStartingDifficulty(profile); got != imodel.DifficultyEasy {
		t.Fatalf("low-score difficulty = %s, want easy", got)
	}
	profile.AbilityScores[imodel.AbilityCommunication] = 0.85
	if got := RecommendStartingDifficulty(profile); got != imodel.DifficultyHard {
		t.Fatalf("high-score difficulty = %s, want hard", got)
	}
}

func resultProfile(store memory.Store, ctx context.Context, t *testing.T, studentID string) *imodel.StudentAbilityProfile {
	t.Helper()
	profile, err := store.LoadAbilityProfile(ctx, studentID)
	if err != nil {
		t.Fatalf("LoadAbilityProfile() error = %v", err)
	}
	return profile
}

func assertAbilityScore(t *testing.T, scores map[string]float64, ability string, want float64) {
	t.Helper()
	if got := scores[ability]; math.Abs(got-want) > 0.0001 {
		t.Fatalf("score[%s] = %.4f, want %.4f", ability, got, want)
	}
}
