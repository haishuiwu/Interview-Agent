/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const abilityEvaluatorPrompt = `你是一名学生能力发展评估专家。请根据学生的完整训练表现，生成形成性的能力诊断报告。

报告只用于训练反馈与成长规划，不对学生作人格或职业定性。评分必须能从实际作答中找到证据；样本不足时应在 summary 中说明。

你只负责分析表现和给出评价依据，不得输出或决定 overall_score、overall_level、ability_scores；最终分数由 Go Service 根据真实训练记录聚合。

请只输出纯 JSON：
{
  "strengths": ["有作答证据支撑的优势"],
  "weaknesses": ["可通过训练改善的具体能力"],
  "detailed_review": [
    {
      "question_content": "题目内容",
      "user_answer": "学生回答摘要",
      "comment": "基于证据的点评与下一步建议",
      "key_points_hit": ["已体现的能力点"],
      "key_points_missed": ["待补充的能力点"]
    }
  ],
  "summary": "综合诊断与下一步训练建议"
}

不得根据印象补写分数，只能引用学生实际回答中的证据。`

// AbilityEvaluator 负责生成学生能力训练评估报告。
type AbilityEvaluator struct {
	chatModel model.ChatModel
}

// NewAbilityEvaluator 创建能力评估 Agent。
func NewAbilityEvaluator(chatModel model.ChatModel) *AbilityEvaluator {
	return &AbilityEvaluator{chatModel: chatModel}
}

// Evaluate 生成训练评估报告。userTerminated 表示训练是否由用户主动终止。
func (e *AbilityEvaluator) Evaluate(ctx context.Context, state *imodel.TrainingState, standard *imodel.AbilityStandard, profile *imodel.StudentProfile, userTerminated bool) (*imodel.EvaluationReport, error) {
	// 构建训练过程摘要
	var qaText string
	evaluatedAttempts := evaluatedTrainingAttempts(state, true)
	if len(evaluatedAttempts) > 0 {
		for i, attempt := range evaluatedAttempts {
			attemptLabel := "主任务"
			if attempt.ParentAttemptID != "" || attempt.AttemptType == imodel.TrainingAttemptTypeFollowUp {
				attemptLabel = "追问"
			}
			qaText += fmt.Sprintf("### 第 %d 次作答（%s / %s）\n", i+1, attemptLabel, attempt.SkillName)
			qaText += fmt.Sprintf("**训练任务**：%s\n", attempt.TrainingTask)
			qaText += fmt.Sprintf("**实际题目**：%s\n", attempt.Question)
			qaText += fmt.Sprintf("**回答**：%s\n", attempt.Answer)
			qaText += fmt.Sprintf("**即时得分**：%.0f\n\n", attempt.EvaluationResult.Score)
		}
	} else {
		for i, qa := range state.QAHistory {
			qaText += fmt.Sprintf("### 第 %d 题（%s / %s）\n", i+1, qa.Question.Type, qa.Question.Difficulty)
			qaText += fmt.Sprintf("**题目**：%s\n", qa.Question.Content)
			qaText += fmt.Sprintf("**回答**：%s\n", qa.UserAnswer)
			qaText += fmt.Sprintf("**即时得分**：%.0f\n\n", qa.Score)
		}
	}
	completedCount := completedPrimaryAttemptCount(state)
	if completedCount == 0 {
		completedCount = len(state.QAHistory)
	}

	terminatedNote := ""
	if userTerminated {
		terminatedNote = fmt.Sprintf("\n\n> **注意：本次训练由学生主动终止。原计划 %d 道题，实际完成 %d 道题。请在综合评语中说明训练未完成，诊断仅基于已作答内容。**\n",
			state.TotalQuestions, completedCount)
	}

	userMsg := fmt.Sprintf("## 训练信息\n- 学习目标：%s\n- 学生：%s\n- 年级：%s\n- 学科：%s\n- 计划题目数：%d\n- 实际完成：%d\n- 训练状态：%s%s\n\n## 训练过程\n\n%s",
		standard.LearningGoal, profile.Name, profile.Grade, profile.Subject, state.TotalQuestions, completedCount,
		func() string {
			if userTerminated {
				return "学生主动终止"
			}
			return "正常完成"
		}(), terminatedNote, qaText)

	messages := []*schema.Message{
		schema.SystemMessage(abilityEvaluatorPrompt),
		schema.UserMessage(userMsg),
	}

	resp, err := e.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("ability_evaluator: generate: %w", err)
	}

	result := &imodel.EvaluationReport{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("ability_evaluator: parse response: %w\nraw: %s", err, resp.Content)
	}

	result.SessionID = state.SessionID
	result.StudentID = profile.StudentID
	result.StudentName = profile.Name
	result.Grade = profile.Grade
	result.Subject = profile.Subject
	result.LearningGoal = standard.LearningGoal
	result.TrainingMetrics = calculateTrainingMetrics(state)
	result.AbilityScores, result.OverallScore = calculateFinalAbilityScores(state)
	result.OverallLevel = abilityLevel(result.OverallScore)
	result.DetailedReview = authoritativeQuestionReviews(result.DetailedReview, state)
	result.TrainingAttempts = append([]*imodel.TrainingAttempt(nil), state.TrainingAttempts...)
	result.CreatedAt = time.Now()

	return result, nil
}

// calculateFinalAbilityScores 只基于已记录的逐题事实计算最终分数，不采纳 LLM 输出的分数。
func calculateFinalAbilityScores(state *imodel.TrainingState) (map[string]float64, float64) {
	attempts := evaluatedTrainingAttempts(state, false)
	if len(attempts) > 0 {
		totals := make(map[string]float64)
		counts := make(map[string]int)
		var overallTotal float64
		for _, attempt := range attempts {
			score := math.Max(0, math.Min(100, attempt.EvaluationResult.Score))
			overallTotal += score
			abilities := abilityDimensionsForAttempt(attempt)
			if len(abilities) == 0 {
				abilities = []string{imodel.AbilityProblemSolving}
			}
			for _, ability := range abilities {
				totals[ability] += score
				counts[ability]++
			}
		}
		result := averageAbilityScores(totals, counts)
		overall := math.Round(overallTotal/float64(len(attempts))*100) / 100
		return result, overall
	}

	totals := make(map[string]float64)
	counts := make(map[string]int)
	var overallTotal float64
	for _, qa := range state.QAHistory {
		score := math.Max(0, math.Min(100, qa.Score))
		overallTotal += score
		abilities := abilitiesForQuestion(qa.Question)
		for _, ability := range abilities {
			totals[ability] += score
			counts[ability]++
		}
	}

	result := averageAbilityScores(totals, counts)
	if len(state.QAHistory) == 0 {
		return result, 0
	}
	overall := math.Round(overallTotal/float64(len(state.QAHistory))*100) / 100
	return result, overall
}

func averageAbilityScores(totals map[string]float64, counts map[string]int) map[string]float64 {
	result := make(map[string]float64, len(totals))
	for ability, total := range totals {
		result[ability] = math.Round(total/float64(counts[ability])*100) / 100
	}
	return result
}

func evaluatedTrainingAttempts(state *imodel.TrainingState, includeFollowUps bool) []*imodel.TrainingAttempt {
	if state == nil {
		return nil
	}
	result := make([]*imodel.TrainingAttempt, 0, len(state.TrainingAttempts))
	for _, attempt := range state.TrainingAttempts {
		if attempt == nil || attempt.EvaluationResult == nil {
			continue
		}
		if !includeFollowUps && (attempt.ParentAttemptID != "" || attempt.AttemptType == imodel.TrainingAttemptTypeFollowUp) {
			continue
		}
		result = append(result, attempt)
	}
	return result
}

func completedPrimaryAttemptCount(state *imodel.TrainingState) int {
	return len(evaluatedTrainingAttempts(state, false))
}

func authoritativeQuestionReviews(generated []imodel.QuestionReview, state *imodel.TrainingState) []imodel.QuestionReview {
	attempts := evaluatedTrainingAttempts(state, true)
	if len(attempts) > 0 {
		result := make([]imodel.QuestionReview, len(attempts))
		for i, attempt := range attempts {
			if i < len(generated) {
				result[i] = generated[i]
			}
			evaluation := attempt.EvaluationResult
			result[i].AttemptID = attempt.ID
			result[i].QuestionContent = attempt.Question
			result[i].UserAnswer = attempt.Answer
			result[i].Score = evaluation.Score
			result[i].KeyPointsHit = append([]string(nil), evaluation.KeyPointsHit...)
			result[i].KeyPointsMissed = append([]string(nil), evaluation.KeyPointsMissed...)
			if strings.TrimSpace(result[i].Comment) == "" {
				result[i].Comment = evaluation.Feedback
			}
		}
		return result
	}

	result := make([]imodel.QuestionReview, len(state.QAHistory))
	for i, qa := range state.QAHistory {
		if i < len(generated) {
			result[i] = generated[i]
		}
		result[i].AttemptID = qa.AttemptID
		result[i].QuestionContent = qa.Question.Content
		result[i].UserAnswer = qa.UserAnswer
		result[i].Score = qa.Score
		if strings.TrimSpace(result[i].Comment) == "" {
			result[i].Comment = qa.Feedback
		}
	}
	return result
}

func abilitiesForQuestion(question imodel.PlannedQuestion) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range append(append([]string{}, question.Skills...), question.Content) {
		ability := imodel.NormalizeAbilityDimension(value)
		if ability != "" && !seen[ability] {
			seen[ability] = true
			result = append(result, ability)
		}
	}
	if len(result) > 0 {
		return result
	}
	switch imodel.NormalizeQuestionType(strings.TrimSpace(question.Type)) {
	case imodel.QuestionTypeTheory:
		return []string{imodel.AbilityLogicalThinking}
	case imodel.QuestionTypeScenario:
		return []string{imodel.AbilityCriticalThinking}
	default:
		return []string{imodel.AbilityProblemSolving}
	}
}

func abilityLevel(score float64) string {
	switch {
	case score >= 85:
		return "A"
	case score >= 70:
		return "B"
	case score >= 50:
		return "C"
	default:
		return "D"
	}
}

// FormatReport 将评估报告格式化为 Markdown
func FormatReport(report *imodel.EvaluationReport) string {
	md := fmt.Sprintf("# 学生能力训练评估报告\n\n")
	md += fmt.Sprintf("- **学生**：%s\n", report.StudentName)
	md += fmt.Sprintf("- **学习目标**：%s\n", report.LearningGoal)
	md += fmt.Sprintf("- **综合得分**：%.1f / 100（%s）\n", report.OverallScore, report.OverallLevel)
	md += fmt.Sprintf("- **评估时间**：%s\n\n", report.CreatedAt.Format("2006-01-02 15:04"))

	if len(report.TrainingMetrics) > 0 {
		md += "## 本轮训练量化指标\n\n"
		md += "| 指标 | 数值 |\n|------|------|\n"
		metricLabels := []struct {
			key   string
			label string
			unit  string
		}{
			{"completion_rate", "训练完成率", "%"},
			{"average_score", "平均作答得分", " 分"},
			{"follow_up_rate", "启发式追问触发率", "%"},
			{"question_bank_hit_rate", "题库命中率", "%"},
			{"task_type_coverage", "训练题型覆盖率", "%"},
		}
		for _, metric := range metricLabels {
			if value, ok := report.TrainingMetrics[metric.key]; ok {
				md += fmt.Sprintf("| %s | %.1f%s |\n", metric.label, value, metric.unit)
			}
		}
		md += "\n"
	}

	md += "## 各维度得分\n\n"
	md += "| 维度 | 得分 |\n|------|------|\n"
	for dim, score := range report.AbilityScores {
		md += fmt.Sprintf("| %s | %.1f |\n", dim, score)
	}

	md += "\n## 优势\n\n"
	for _, s := range report.Strengths {
		md += fmt.Sprintf("- %s\n", s)
	}

	md += "\n## 待提升\n\n"
	for _, w := range report.Weaknesses {
		md += fmt.Sprintf("- %s\n", w)
	}

	md += "\n## 逐题点评\n\n"
	for i, review := range report.DetailedReview {
		md += fmt.Sprintf("### 第 %d 题（%.0f分）\n", i+1, review.Score)
		md += fmt.Sprintf("**题目**：%s\n\n", review.QuestionContent)
		md += fmt.Sprintf("**点评**：%s\n\n", review.Comment)
	}

	md += fmt.Sprintf("\n## 综合评语\n\n%s\n", report.Summary)

	return md
}

func calculateTrainingMetrics(state *imodel.TrainingState) map[string]float64 {
	metrics := map[string]float64{
		"completion_rate":        0,
		"average_score":          0,
		"follow_up_rate":         0,
		"question_bank_hit_rate": 0,
		"task_type_coverage":     0,
	}
	answered := len(state.QAHistory)
	if state.TotalQuestions > 0 {
		metrics["completion_rate"] = float64(answered) / float64(state.TotalQuestions) * 100
	}
	if answered == 0 {
		return metrics
	}

	var totalScore float64
	followUps := 0
	bankHits := 0
	types := make(map[string]bool)
	for _, qa := range state.QAHistory {
		totalScore += qa.Score
		if qa.FollowUpUsed {
			followUps++
		}
		if qa.Question.Source != "" && qa.Question.Source != "llm" {
			bankHits++
		}
		types[imodel.NormalizeQuestionType(qa.Question.Type)] = true
	}
	metrics["average_score"] = totalScore / float64(answered)
	metrics["follow_up_rate"] = float64(followUps) / float64(answered) * 100
	metrics["question_bank_hit_rate"] = float64(bankHits) / float64(answered) * 100
	metrics["task_type_coverage"] = float64(len(types)) / 3 * 100
	return metrics
}
