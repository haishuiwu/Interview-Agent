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
	Name           string  `json:"name"`
	Ability        string  `json:"ability,omitempty"`
	KnowledgePoint string  `json:"knowledge_point,omitempty"`
	Description    string  `json:"description,omitempty"`
	Weight         float64 `json:"weight,omitempty"`
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

// TrainingAttempt 是一次训练从任务、实际提问、作答到评价和学习状态变化的唯一事实记录。
type TrainingAttempt struct {
	ID string `json:"id"`

	StudentID string `json:"student_id"`

	SkillName       string   `json:"skill_name"`
	TrainingTask    string   `json:"training_task"`
	Subject         string   `json:"subject,omitempty"`
	Chapter         string   `json:"chapter,omitempty"`
	KnowledgePoints []string `json:"knowledge_points,omitempty"`
	Difficulty      string   `json:"difficulty,omitempty"`

	Question        string `json:"question"`
	ReferenceAnswer string `json:"reference_answer"`

	Rubric []EvaluationCriterion `json:"rubric"`

	Answer string `json:"answer"`

	EvaluationResult *EvaluationResult `json:"evaluation_result,omitempty"`

	AbilityChanges          map[string]float64 `json:"ability_changes,omitempty"`
	KnowledgeMasteryChanges map[string]float64 `json:"knowledge_mastery_changes,omitempty"`
	MasteryEvidenceScore    float64            `json:"mastery_evidence_score,omitempty"`
	HintUsed                bool               `json:"hint_used,omitempty"`
	FollowUpCount           int                `json:"follow_up_count,omitempty"`

	Status string `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// ParentAttemptID 与 AttemptType 用于保留追问关系；主训练事实的 ParentAttemptID 为空。
	ParentAttemptID string `json:"parent_attempt_id,omitempty"`
	AttemptType     string `json:"attempt_type,omitempty"`
}

// EvaluationRubricForQuestion 将规划题目的能力点固化为本次训练使用的评价量规。
func EvaluationRubricForQuestion(question PlannedQuestion) []EvaluationCriterion {
	if knowledgePoint := strings.TrimSpace(question.KnowledgePoint); knowledgePoint != "" {
		ability := AbilityProblemSolving
		switch NormalizeQuestionType(strings.TrimSpace(question.Type)) {
		case QuestionTypeTheory:
			ability = AbilityLogicalThinking
		case QuestionTypeScenario:
			ability = AbilityCriticalThinking
		}
		return []EvaluationCriterion{{
			Name:           knowledgePoint,
			Ability:        ability,
			KnowledgePoint: knowledgePoint,
			Description:    fmt.Sprintf("评估学生是否真正理解并能应用知识点「%s」，包括概念、条件、推理过程与典型误区", knowledgePoint),
			Weight:         1,
		}}
	}

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
	if strings.TrimSpace(question.KnowledgePoint) != "" {
		switch NormalizeQuestionType(question.Type) {
		case QuestionTypeTheory:
			return "concept-tutor"
		case QuestionTypeScenario:
			return "knowledge-compare"
		default:
			return "guided-practice"
		}
	}
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

// RecordAbilityChanges 记录兼容评价维度的确定性变化；知识点掌握变化由 RecordKnowledgeMasteryChanges 记录。
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

// EvidenceScore 返回用于更新掌握度的确定性证据分；提示后的表现会折损。
func (a *TrainingAttempt) EvidenceScore() float64 {
	if a == nil {
		return 0
	}
	score := a.MasteryEvidenceScore
	if score <= 0 && a.EvaluationResult != nil {
		score = a.EvaluationResult.Score
	}
	if a.HintUsed && a.MasteryEvidenceScore <= 0 {
		score *= 0.85
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// RecordKnowledgeMasteryChanges 记录一次作答对各知识点掌握度造成的变化。
func (a *TrainingAttempt) RecordKnowledgeMasteryChanges(changes map[string]float64) {
	if a == nil {
		return
	}
	a.KnowledgeMasteryChanges = make(map[string]float64, len(changes))
	for knowledgePoint, change := range changes {
		a.KnowledgeMasteryChanges[knowledgePoint] = change
	}
	a.UpdatedAt = time.Now()
}
