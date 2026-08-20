package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/agent"
	imodel "interview-agent/internal/model"
)

type abilityDiagnosisCase struct {
	ID              string   `json:"id"`
	Question        string   `json:"question"`
	Answer          string   `json:"answer"`
	Reference       string   `json:"reference"`
	Skill           string   `json:"skill"`
	QuestionType    string   `json:"question_type"`
	Hit             []string `json:"hit"`
	Missed          []string `json:"missed"`
	ExpectedAbility string   `json:"expected_ability"`
	ExpectedScore   float64  `json:"expected_score"`
}

type diagnosisMockModel struct {
	responses []string
	index     int
}

func newDiagnosisMock(t *testing.T, sample abilityDiagnosisCase) *diagnosisMockModel {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"feedback":          "基于学生实际回答形成的证据反馈",
		"key_points_hit":    sample.Hit,
		"key_points_missed": sample.Missed,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := json.Marshal(map[string]any{
		"strengths":  []string{"已覆盖的能力点有明确作答证据"},
		"weaknesses": []string{"遗漏点需要继续训练"},
		"detailed_review": []map[string]any{{
			"comment":           "依据命中点和遗漏点形成诊断",
			"key_points_hit":    sample.Hit,
			"key_points_missed": sample.Missed,
		}},
		"summary": "本报告中的数值分数由 Go 聚合。",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &diagnosisMockModel{responses: []string{string(evidence), string(report)}}
}

func (m *diagnosisMockModel) BindTools([]*schema.ToolInfo) error { return nil }

func (m *diagnosisMockModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.index >= len(m.responses) {
		return nil, fmt.Errorf("unexpected diagnosis mock call %d", m.index)
	}
	response := schema.AssistantMessage(m.responses[m.index], nil)
	m.index++
	return response, nil
}

func (m *diagnosisMockModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestAbilityDiagnosisBenchmark(t *testing.T) {
	cases := loadFixture[[]abilityDiagnosisCase](t, "ability_diagnosis.json")
	passed := 0
	for _, sample := range cases {
		mockLLM := newDiagnosisMock(t, sample)
		coach := agent.NewStudentCoach(mockLLM)
		question := &imodel.PlannedQuestion{
			Content:    sample.Question,
			Reference:  sample.Reference,
			Type:       sample.QuestionType,
			Difficulty: string(imodel.DifficultyMedium),
			Skills:     []string{sample.Skill},
		}
		score, err := coach.ScoreAnswer(context.Background(), question, sample.Answer)
		if err != nil {
			t.Logf("[%s] ScoreAnswer error: %v", sample.ID, err)
			continue
		}
		state := &imodel.TrainingState{
			SessionID:      "diagnosis-" + sample.ID,
			TotalQuestions: 1,
			QAHistory: []imodel.QAPair{{
				Question:   *question,
				UserAnswer: sample.Answer,
				Score:      score.Score,
				Feedback:   score.Feedback,
			}},
		}
		evaluator := agent.NewAbilityEvaluator(mockLLM)
		report, err := evaluator.Evaluate(context.Background(), state,
			&imodel.AbilityStandard{LearningGoal: "能力诊断基准"},
			&imodel.StudentProfile{StudentID: "diagnosis-student", Name: "测试学生"}, false)
		if err != nil {
			t.Logf("[%s] Evaluate error: %v", sample.ID, err)
			continue
		}
		actualScore, abilityFound := report.AbilityScores[sample.ExpectedAbility]
		if abilityFound && math.Abs(actualScore-sample.ExpectedScore) < 0.01 && math.Abs(score.Score-sample.ExpectedScore) < 0.01 {
			passed++
		} else {
			t.Logf("[%s] expected ability=%s score=%.2f, report=%v question_score=%.2f", sample.ID, sample.ExpectedAbility, sample.ExpectedScore, report.AbilityScores, score.Score)
		}
	}

	accuracy := reportMetric(t, "Diagnosis Accuracy", passed, len(cases))
	if accuracy < 0.90 {
		t.Fatalf("Diagnosis Accuracy %.2f%% is below 90%%", accuracy*100)
	}
}
