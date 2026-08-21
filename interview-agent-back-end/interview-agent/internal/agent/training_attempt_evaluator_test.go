package agent

import (
	"testing"

	imodel "interview-agent/internal/model"
)

func TestEvaluationUsesTrainingAttemptInsteadOfConflictingQA(t *testing.T) {
	attempt := &imodel.TrainingAttempt{
		ID:           "attempt-primary",
		SkillName:    "logical-thinking",
		TrainingTask: "规划任务 A",
		Question:     "实际问题 B",
		Answer:       "实际回答",
		Rubric: []imodel.EvaluationCriterion{{
			Name: "逻辑思考", Ability: imodel.AbilityLogicalThinking, Weight: 1,
		}},
		EvaluationResult: &imodel.EvaluationResult{
			Score: 90, Feedback: "评价事实", KeyPointsHit: []string{"推理"},
		},
		AttemptType: imodel.TrainingAttemptTypePrimary,
	}
	state := &imodel.TrainingState{
		TrainingAttempts: []*imodel.TrainingAttempt{attempt},
		QAHistory: []imodel.QAPair{{
			Question:   imodel.PlannedQuestion{Content: "冲突问题 C", Skills: []string{"communication-training"}},
			UserAnswer: "冲突回答",
			Score:      10,
		}},
	}

	scores, overall := calculateFinalAbilityScores(state)
	assertMetric(t, scores, imodel.AbilityLogicalThinking, 90)
	if _, exists := scores[imodel.AbilityCommunication]; exists {
		t.Fatalf("conflicting QA ability was used: %#v", scores)
	}
	if overall != 90 {
		t.Fatalf("overall = %v, want 90", overall)
	}

	reviews := authoritativeQuestionReviews([]imodel.QuestionReview{{
		QuestionContent: "LLM 改写的问题", UserAnswer: "LLM 改写的回答", Score: 1,
	}}, state)
	if len(reviews) != 1 || reviews[0].AttemptID != attempt.ID || reviews[0].QuestionContent != attempt.Question || reviews[0].UserAnswer != attempt.Answer || reviews[0].Score != 90 {
		t.Fatalf("authoritative reviews = %#v", reviews)
	}
}
