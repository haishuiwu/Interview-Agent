package graph

import (
	"testing"

	imodel "interview-agent/internal/model"
)

func TestNewTrainingAttemptSeparatesTaskFromPresentedQuestion(t *testing.T) {
	planned := imodel.PlannedQuestion{
		Content:   "规划任务 A",
		Skills:    []string{"communication-training"},
		Reference: "参考答案 A",
	}
	attempt := newTrainingAttempt("student-001", planned, "实际展示问题 B", planned.Reference, imodel.TrainingAttemptTypePrimary, "")

	if attempt.ID == "" || attempt.StudentID != "student-001" {
		t.Fatalf("attempt identity = %#v", attempt)
	}
	if attempt.TrainingTask != planned.Content || attempt.Question != "实际展示问题 B" {
		t.Fatalf("task/question binding = %#v", attempt)
	}
	if attempt.SkillName != "communication-training" || len(attempt.Rubric) != 1 {
		t.Fatalf("skill/rubric binding = %#v", attempt)
	}
	if attempt.Status != imodel.TrainingAttemptStatusPresented || attempt.CreatedAt.IsZero() || attempt.UpdatedAt.IsZero() {
		t.Fatalf("attempt lifecycle = %#v", attempt)
	}
}
