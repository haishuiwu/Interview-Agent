package agent

import (
	"math"
	"testing"

	imodel "interview-agent/internal/model"
)

func TestCalculateTrainingMetricsUsesSessionFacts(t *testing.T) {
	state := &imodel.InterviewState{
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
	metrics := calculateTrainingMetrics(&imodel.InterviewState{})
	for key, value := range metrics {
		if value != 0 {
			t.Fatalf("metric %s = %v, want 0", key, value)
		}
	}
}

func assertMetric(t *testing.T, metrics map[string]float64, key string, want float64) {
	t.Helper()
	if got := metrics[key]; math.Abs(got-want) > 0.001 {
		t.Fatalf("metric %s = %v, want %v", key, got, want)
	}
}
