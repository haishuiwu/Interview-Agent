package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	TrainingAttemptStatusPresented = "presented"
	TrainingAttemptStatusAnswered  = "answered"
	TrainingAttemptStatusEvaluated = "evaluated"
)

const (
	TrainingAttemptTypePrimary  = "primary"
	TrainingAttemptTypeFollowUp = "follow_up"
)

// EvaluationCriterion 描述一次训练作答所使用的单项评价标准。
type EvaluationCriterion struct {
	Name        string  `json:"name"`
	Ability     string  `json:"ability,omitempty"`
	Description string  `json:"description,omitempty"`
	Weight      float64 `json:"weight,omitempty"`
}

// EvaluationResult 是一次训练作答的评价事实。
// 分数由 Go 根据命中与遗漏的评价证据计算，LLM 只负责提取证据和生成反馈。
type EvaluationResult struct {
	Score           float64   `json:"score"`
	Feedback        string    `json:"feedback"`
	KeyPointsHit    []string  `json:"key_points_hit"`
	KeyPointsMissed []string  `json:"key_points_missed"`
	ShouldFollowUp  bool      `json:"should_follow_up"`
	EvaluatedAt     time.Time `json:"evaluated_at"`
}

// TrainingAttempt 是一次训练从任务、实际提问、作答到评价和能力变化的唯一事实记录。
type TrainingAttempt struct {
	ID string `json:"id"`

	StudentID string `json:"student_id"`

	SkillName    string `json:"skill_name"`
	TrainingTask string `json:"training_task"`

	Question        string `json:"question"`
	ReferenceAnswer string `json:"reference_answer"`

	Rubric []EvaluationCriterion `json:"rubric"`

	Answer string `json:"answer"`

	EvaluationResult *EvaluationResult `json:"evaluation_result,omitempty"`

	AbilityChanges map[string]float64 `json:"ability_changes,omitempty"`

	Status string `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// ParentAttemptID 与 AttemptType 用于保留追问关系；主训练事实的 ParentAttemptID 为空。
	ParentAttemptID string `json:"parent_attempt_id,omitempty"`
	AttemptType     string `json:"attempt_type,omitempty"`
}

// EvaluationRubricForQuestion 将规划题目的能力点固化为本次训练使用的评价量规。
func EvaluationRubricForQuestion(question PlannedQuestion) []EvaluationCriterion {
	abilities := make([]string, 0, len(question.Skills))
	seen := make(map[string]bool)
	for _, skill := range question.Skills {
		ability := NormalizeAbilityDimension(skill)
		if ability == "" || seen[ability] {
			continue
		}
		seen[ability] = true
		abilities = append(abilities, ability)
	}
	if len(abilities) == 0 {
		switch NormalizeQuestionType(strings.TrimSpace(question.Type)) {
		case QuestionTypeTheory:
			abilities = append(abilities, AbilityLogicalThinking)
		case QuestionTypeScenario:
			abilities = append(abilities, AbilityCriticalThinking)
		default:
			abilities = append(abilities, AbilityProblemSolving)
		}
	}

	weight := 1 / float64(len(abilities))
	rubric := make([]EvaluationCriterion, 0, len(abilities))
	for _, ability := range abilities {
		rubric = append(rubric, EvaluationCriterion{
			Name:        ability,
			Ability:     ability,
			Description: fmt.Sprintf("评估学生是否通过实际作答体现 %s 的思考过程与依据", ability),
			Weight:      weight,
		})
	}
	return rubric
}

// SkillNameForQuestion 返回与题目首要评价能力对应的训练 Skill。
func SkillNameForQuestion(question PlannedQuestion) string {
	rubric := EvaluationRubricForQuestion(question)
	if len(rubric) == 0 {
		return AbilitySkillName(AbilityProblemSolving)
	}
	return AbilitySkillName(rubric[0].Ability)
}

// RecordAnswer 将学生回答绑定到当前训练事实。
func (a *TrainingAttempt) RecordAnswer(answer string) {
	if a == nil {
		return
	}
	a.Answer = answer
	a.Status = TrainingAttemptStatusAnswered
	a.UpdatedAt = time.Now()
}

// RecordEvaluation 将评分结果绑定到当前训练事实。
func (a *TrainingAttempt) RecordEvaluation(result *EvaluationResult) {
	if a == nil || result == nil {
		return
	}
	if result.EvaluatedAt.IsZero() {
		result.EvaluatedAt = time.Now()
	}
	a.EvaluationResult = result
	a.Status = TrainingAttemptStatusEvaluated
	a.UpdatedAt = result.EvaluatedAt
}

// RecordAbilityChanges 记录本次评价对训练中能力画像造成的确定性变化。
func (a *TrainingAttempt) RecordAbilityChanges(changes map[string]float64) {
	if a == nil {
		return
	}
	a.AbilityChanges = make(map[string]float64, len(changes))
	for ability, change := range changes {
		a.AbilityChanges[ability] = change
	}
	a.UpdatedAt = time.Now()
}
