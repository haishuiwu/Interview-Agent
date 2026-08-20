package agent

import (
	"math"
	"testing"

	imodel "interview-agent/internal/model"
)

func TestCalculateTrainingMetricsUsesSessionFacts(t *testing.T) {
	state := &imodel.TrainingState{
		TotalQuestions: 4,
		QAHistory: []imodel.QAPair{
			{
				Question:     imodel.PlannedQuestion{Type: "basic", Source: "teacher_theory_e01"},
				Score:        80,
				FollowUpUsed: true,
			},
			{
				Question: imodel.PlannedQuestion{Type: imodel.QuestionTypePractice, Source: "llm"},
				Score:    60,
			},
		},
	}

	metrics := calculateTrainingMetrics(state)
	assertMetric(t, metrics, "completion_rate", 50)
	assertMetric(t, metrics, "average_score", 70)
	assertMetric(t, metrics, "follow_up_rate", 50)
	assertMetric(t, metrics, "question_bank_hit_rate", 50)
	assertMetric(t, metrics, "task_type_coverage", 200.0/3.0)
}

func TestCalculateTrainingMetricsHandlesEmptySession(t *testing.T) {
	metrics := calculateTrainingMetrics(&imodel.TrainingState{})
	for key, value := range metrics {
		if value != 0 {
			t.Fatalf("metric %s = %v, want 0", key, value)
		}
	}
}

func TestCalculateFinalAbilityScoresUsesQAHistory(t *testing.T) {
	state := &imodel.TrainingState{QAHistory: []imodel.QAPair{
		{Question: imodel.PlannedQuestion{Skills: []string{"communication-training"}}, Score: 55},
		{Question: imodel.PlannedQuestion{Skills: []string{"表达清晰度"}}, Score: 75},
		{Question: imodel.PlannedQuestion{Skills: []string{"logical-thinking"}}, Score: 80},
	}}

	scores, overall := calculateFinalAbilityScores(state)
	assertMetric(t, scores, imodel.AbilityCommunication, 65)
	assertMetric(t, scores, imodel.AbilityLogicalThinking, 80)
	if math.Abs(overall-70) > 0.001 {
		t.Fatalf("overall = %v, want 70", overall)
	}
	if got := abilityLevel(overall); got != "B" {
		t.Fatalf("abilityLevel(%v) = %q, want B", overall, got)
	}
}

func assertMetric(t *testing.T, metrics map[string]float64, key string, want float64) {
	t.Helper()
	if got := metrics[key]; math.Abs(got-want) > 0.001 {
		t.Fatalf("metric %s = %v, want %v", key, got, want)
	}
}
