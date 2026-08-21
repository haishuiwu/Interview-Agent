package model

import "testing"

func TestTrainingAttemptLifecycleKeepsSingleFact(t *testing.T) {
	attempt := &TrainingAttempt{Status: TrainingAttemptStatusPresented}
	attempt.RecordAnswer("学生的实际回答")
	if attempt.Status != TrainingAttemptStatusAnswered || attempt.Answer != "学生的实际回答" {
		t.Fatalf("answered attempt = %#v", attempt)
	}

	evaluation := &EvaluationResult{Score: 75, Feedback: "继续补充验证步骤"}
	attempt.RecordEvaluation(evaluation)
	if attempt.Status != TrainingAttemptStatusEvaluated || attempt.EvaluationResult != evaluation {
		t.Fatalf("evaluated attempt = %#v", attempt)
	}
	if evaluation.EvaluatedAt.IsZero() {
		t.Fatal("evaluated_at was not recorded")
	}

	attempt.RecordAbilityChanges(map[string]float64{AbilityCommunication: 0.1})
	if got := attempt.AbilityChanges[AbilityCommunication]; got != 0.1 {
		t.Fatalf("ability change = %v, want 0.1", got)
	}
}

func TestEvaluationRubricForQuestionMapsToSkill(t *testing.T) {
	question := PlannedQuestion{Skills: []string{"表达清晰度", "communication-training"}}
	rubric := EvaluationRubricForQuestion(question)
	if len(rubric) != 1 || rubric[0].Ability != AbilityCommunication {
		t.Fatalf("rubric = %#v", rubric)
	}
	if got := SkillNameForQuestion(question); got != "communication-training" {
		t.Fatalf("skill name = %q", got)
	}
}
