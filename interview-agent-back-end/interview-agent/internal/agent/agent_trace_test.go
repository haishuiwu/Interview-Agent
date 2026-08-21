package agent

import (
	"context"
	"testing"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
	educationtool "interview-agent/internal/tool"
)

func TestAgentTraceRecordsReadToolCalls(t *testing.T) {
	growthService := &studentCoachToolScenarioService{}
	registry, err := educationtool.NewEducationRegistry(growthService)
	if err != nil {
		t.Fatalf("NewEducationRegistry() error = %v", err)
	}
	traceService := service.NewAgentTraceService(memory.NewMemoryStore())
	if _, err := traceService.Create(context.Background(), "tool-trace-session", "student-001", "improve communication"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	ctx := educationtool.WithRuntime(context.Background(), "student-001", growthService)
	ctx = educationtool.WithTrace(ctx, "tool-trace-session", traceService)
	coach := NewStudentCoach(&studentCoachToolCallingModel{}, WithEducationToolRegistry(registry))
	_, err = coach.AskQuestion(ctx, &imodel.TrainingState{
		CurrentQuestion: 1, TotalQuestions: 1, CurrentDifficulty: imodel.DifficultyEasy,
	}, &imodel.PlannedQuestion{
		Content: "围绕学生的表达能力安排一个训练任务。",
		Skills:  []string{"communication-training"},
	}, "我想提升表达能力")
	if err != nil {
		t.Fatalf("AskQuestion() error = %v", err)
	}

	trace, err := traceService.GetBySessionID(ctx, "tool-trace-session")
	if err != nil {
		t.Fatalf("GetBySessionID() error = %v", err)
	}
	want := map[string]bool{"get_student_profile": false, "search_training_case": false}
	for _, call := range trace.ToolCalls {
		if _, ok := want[call.Name]; ok && call.Success {
			want[call.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("tool trace missing successful %s: %+v", name, trace.ToolCalls)
		}
	}
	if trace.SelectedSkill != "communication-training" {
		t.Fatalf("selected skill = %q, want recommendation to update Trace", trace.SelectedSkill)
	}
}
