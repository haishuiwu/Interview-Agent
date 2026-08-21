package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

type trainingAttemptScoreModel struct {
	prompt   string
	response string
}

func (m *trainingAttemptScoreModel) BindTools(_ []*schema.ToolInfo) error {
	return nil
}

func (m *trainingAttemptScoreModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.prompt = input[len(input)-1].Content
	response := m.response
	if response == "" {
		response = `{"feedback":"需要补充验证","key_points_hit":["观点"],"key_points_missed":["验证"]}`
	}
	return schema.AssistantMessage(response, nil), nil
}

func (m *trainingAttemptScoreModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestScoreTrainingAttemptUsesPresentedQuestion(t *testing.T) {
	chatModel := &trainingAttemptScoreModel{}
	coach := NewStudentCoach(chatModel)
	attempt := &imodel.TrainingAttempt{
		TrainingTask:    "规划阶段的任务 A",
		Question:        "实际展示给学生的问题 B",
		ReferenceAnswer: "观点和验证",
		Rubric:          []imodel.EvaluationCriterion{{Name: "逻辑", Ability: imodel.AbilityLogicalThinking, Weight: 1}},
		Answer:          "我先给出观点",
		Status:          imodel.TrainingAttemptStatusAnswered,
	}

	result, err := coach.ScoreTrainingAttempt(context.Background(), attempt)
	if err != nil {
		t.Fatalf("ScoreTrainingAttempt() error = %v", err)
	}
	if !strings.Contains(chatModel.prompt, attempt.Question) {
		t.Fatalf("score prompt does not contain presented question: %s", chatModel.prompt)
	}
	if strings.Contains(chatModel.prompt, attempt.TrainingTask) {
		t.Fatalf("score prompt unexpectedly used planning task: %s", chatModel.prompt)
	}
	if result.Score != 50 || attempt.EvaluationResult != result {
		t.Fatalf("evaluation result = %#v, attempt = %#v", result, attempt)
	}
	if attempt.Status != imodel.TrainingAttemptStatusEvaluated || result.EvaluatedAt.IsZero() {
		t.Fatalf("attempt status = %q, evaluated_at = %s", attempt.Status, result.EvaluatedAt)
	}
}

func TestUpdateStudentAbilityProfileRecordsAttemptChanges(t *testing.T) {
	chatModel := &trainingAttemptScoreModel{response: `{"summary":"表达结构有所体现","strengths":["观点明确"],"weaknesses":["需要补充验证"]}`}
	coach := NewStudentCoach(chatModel)
	attempt := &imodel.TrainingAttempt{
		StudentID: "student-001",
		SkillName: "communication-training",
		Rubric: []imodel.EvaluationCriterion{{
			Name: "表达", Ability: imodel.AbilityCommunication, Weight: 1,
		}},
		EvaluationResult: &imodel.EvaluationResult{Score: 80},
	}
	current := &imodel.StudentAbilityProfile{
		StudentID:     "student-001",
		AbilityScores: map[string]float64{imodel.AbilityCommunication: 0.4},
	}

	updated, err := coach.UpdateStudentAbilityProfileFromAttempt(context.Background(), current, 1, attempt)
	if err != nil {
		t.Fatalf("UpdateStudentAbilityProfileFromAttempt() error = %v", err)
	}
	if got := updated.AbilityScores[imodel.AbilityCommunication]; got != 0.6 {
		t.Fatalf("updated ability = %v, want 0.6", got)
	}
	if got := attempt.AbilityChanges[imodel.AbilityCommunication]; got != 0.2 {
		t.Fatalf("attempt ability change = %v, want 0.2", got)
	}
}
