package evaluation

import (
	"context"
	"math"
	"strings"
	"testing"

	"interview-agent/internal/agent"
	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
	educationtool "interview-agent/internal/tool"
)

type growthLoopCase struct {
	StudentID      string             `json:"student_id"`
	LearningGoal   string             `json:"learning_goal"`
	BeforeScores   map[string]float64 `json:"before_scores"`
	TrainingScores map[string]float64 `json:"training_scores"`
	ExpectedAfter  map[string]float64 `json:"expected_after"`
	ExpectedSkill  string             `json:"expected_skill"`
}

func TestGrowthLoopBenchmark(t *testing.T) {
	sample := loadFixture[growthLoopCase](t, "growth_loop.json")
	ctx := context.Background()
	store := memory.NewMemoryStore()
	if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID:     sample.StudentID,
		AbilityScores: sample.BeforeScores,
	}); err != nil {
		t.Fatalf("seed ability profile: %v", err)
	}

	dataService := service.NewStudentGrowthDataService(store, nil, nil, nil)
	update, err := dataService.UpdateAbilityProfile(ctx, sample.StudentID, service.GrowthRecordInput{
		SessionID:     "growth-loop-first-training",
		LearningGoal:  sample.LearningGoal,
		OverallScore:  75,
		AbilityScores: sample.TrainingScores,
		Weaknesses:    []string{"表达结构仍需巩固"},
		Summary:       "第一次训练已完成。",
	})
	if err != nil {
		t.Fatalf("first training update: %v", err)
	}
	for ability, expected := range sample.ExpectedAfter {
		if actual := update.Profile.AbilityScores[ability]; math.Abs(actual-expected) > 0.0001 {
			t.Fatalf("after[%s] = %.4f, want %.4f", ability, actual, expected)
		}
	}
	if update.GrowthRecord == nil || update.GrowthRecord.SessionID != "growth-loop-first-training" {
		t.Fatalf("GrowthRecord was not saved: %#v", update.GrowthRecord)
	}

	recorder := &recordingGrowthService{delegate: dataService}
	registry, err := educationtool.NewEducationRegistry(recorder)
	if err != nil {
		t.Fatalf("NewEducationRegistry(): %v", err)
	}
	mockLLM := &scriptedToolModel{
		calls:             mockToolPlan("帮我训练一下"),
		requireToolResult: sample.ExpectedSkill,
		finalContent:      "根据长期画像，第二次训练优先推荐 " + sample.ExpectedSkill + "。",
	}
	coach := agent.NewStudentCoach(mockLLM, agent.WithEducationToolRegistry(registry))
	toolCtx := educationtool.WithRuntime(ctx, sample.StudentID, recorder)
	response, err := coach.AskQuestion(toolCtx, &imodel.TrainingState{
		CurrentQuestion:       1,
		TotalQuestions:        1,
		CurrentDifficulty:     imodel.DifficultyMedium,
		StudentAbilityProfile: update.Profile,
	}, &imodel.PlannedQuestion{Content: "根据画像选择训练重点。"}, "帮我训练一下")
	if err != nil {
		t.Fatalf("second training recommendation: %v", err)
	}

	passed := 0
	if strings.Contains(response, sample.ExpectedSkill) && exactChain(recorder.calls, []string{"get_ability_profile", "recommend_training_task"}) {
		passed = 1
	} else {
		t.Logf("expected_skill=%s response=%q calls=%v", sample.ExpectedSkill, response, recorder.calls)
	}
	accuracy := reportMetric(t, "Growth Loop Success Rate", passed, 1)
	if accuracy < 1 {
		t.Fatalf("growth loop benchmark failed")
	}
}
