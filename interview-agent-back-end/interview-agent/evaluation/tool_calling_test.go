package evaluation

import (
	"context"
	"strings"
	"testing"

	"interview-agent/internal/agent"
	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
	educationtool "interview-agent/internal/tool"
)

type toolCallingCase struct {
	ID            string   `json:"id"`
	Input         string   `json:"input"`
	ExpectedTools []string `json:"expected_tools"`
}

func TestToolCallingBenchmark(t *testing.T) {
	cases := loadFixture[[]toolCallingCase](t, "tool_calling.json")
	passed := 0
	for _, sample := range cases {
		ctx := context.Background()
		store := memory.NewMemoryStore()
		if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
			StudentID: "tool-benchmark-student",
			AbilityScores: map[string]float64{
				imodel.AbilityCommunication:   0.55,
				imodel.AbilityLogicalThinking: 0.80,
			},
		}); err != nil {
			t.Fatalf("[%s] seed ability profile: %v", sample.ID, err)
		}

		dataService := service.NewStudentGrowthDataService(store, nil, nil, nil)
		recorder := &recordingGrowthService{delegate: dataService}
		registry, err := educationtool.NewEducationRegistry(recorder)
		if err != nil {
			t.Fatalf("[%s] NewEducationRegistry(): %v", sample.ID, err)
		}
		coach := agent.NewStudentCoach(
			&scriptedToolModel{calls: mockToolPlan(sample.Input)},
			agent.WithEducationToolRegistry(registry),
		)
		toolCtx := educationtool.WithRuntime(ctx, "tool-benchmark-student", recorder)
		_, err = coach.AskQuestion(toolCtx, &imodel.TrainingState{
			CurrentQuestion:   1,
			TotalQuestions:    1,
			CurrentDifficulty: imodel.DifficultyMedium,
		}, &imodel.PlannedQuestion{Content: sample.Input}, sample.Input)
		if err != nil {
			t.Logf("[%s] StudentCoach error: %v", sample.ID, err)
			continue
		}
		if exactChain(recorder.calls, sample.ExpectedTools) {
			passed++
		} else {
			t.Logf("[%s] input=%q expected=%v actual=%v", sample.ID, sample.Input, sample.ExpectedTools, recorder.calls)
		}
	}

	accuracy := reportMetric(t, "Tool Selection Accuracy", passed, len(cases))
	if accuracy < 0.90 {
		t.Fatalf("Tool Selection Accuracy %.2f%% is below 90%%", accuracy*100)
	}
}

func mockToolPlan(input string) []mockToolCall {
	switch {
	case strings.Contains(input, "提升自己的表达能力"):
		return []mockToolCall{
			{name: "get_ability_profile", arguments: `{}`},
			{name: "get_growth_history", arguments: `{"ability":"communication","limit":5}`},
			{name: "search_training_case", arguments: `{"ability_gap":"communication","limit":3}`},
			{name: "recommend_training_task", arguments: `{"learning_goal":"提升表达能力","ability":"communication","case_content":""}`},
		}
	case strings.Contains(input, "帮我训练一下"):
		return []mockToolCall{
			{name: "get_ability_profile", arguments: `{}`},
			{name: "recommend_training_task", arguments: `{"learning_goal":"帮我训练一下","ability":"","case_content":""}`},
		}
	case strings.Contains(input, "最近一次能力报告"):
		return []mockToolCall{{name: "get_ability_report", arguments: `{}`}}
	case strings.Contains(input, "历史表达训练"):
		return []mockToolCall{{name: "get_growth_history", arguments: `{"ability":"communication","limit":5}`}}
	case strings.Contains(input, "训练案例"):
		return []mockToolCall{{name: "search_training_case", arguments: `{"ability_gap":"critical_thinking","limit":3}`}}
	case strings.Contains(input, "当前能力画像"):
		return []mockToolCall{{name: "get_ability_profile", arguments: `{}`}}
	default:
		return nil
	}
}
