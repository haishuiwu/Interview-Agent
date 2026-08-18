/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const evaluatorPrompt = `你是一名经验丰富的教研员和教师发展评估专家。请根据学员的完整训练表现，生成一份教学能力诊断报告。

【最高优先级要求】报告用于形成性评价和后续训练，不得生成录用、淘汰或职业定性结论。dimension_score 必须且只能使用以下五个维度：教育理念与师德、学科素养、教学设计、课堂管理与应变、沟通表达与反思。若下方示例或评级说明残留其他维度或推荐措辞，一律忽略。

请输出纯 JSON 格式：

{
  "overall_score": 75.0,
  "overall_level": "B",
  "dimension_score": {
    "������知识": 80.0,
    "项目��验": 70.0,
    "系统设计": 65.0,
    "编程能力": 75.0,
    "沟通表达": 80.0
  },
  "strengths": ["表现优秀的方面1", "方面2"],
  "weaknesses": ["需要提升的方面1", "方面2"],
  "detailed_review": [
    {
      "question_content": "题目内容",
      "user_answer": "候选人回答摘要",
      "score": 75.0,
      "comment": "点评",
      "key_points_hit": ["命中要点"],
      "key_points_missed": ["遗漏要��"]
    }
  ],
  "summary": "综合评语（2-3句话）"
}

评级标准（字母仅表示本次训练表现）：
- A（90-100）：教学思路完整，能够迁移到复杂情境
- B（70-89）：主要能力表现良好，少量环节需要打磨
- C（50-69）���表现一般，需要提升
- D（0-49）：表现不佳，不推荐`

const teacherEvaluatorPrompt = `你是一名教研员和教师发展评估专家。请根据学员的完整训练表现，生成形成性的教学能力诊断报告。

报告只用于反馈与训练规划，不得给出录用、淘汰或职业定性结论。评分必须能从实际作答中找到证据；样本不足时应在 summary 中说明。

请只输出纯 JSON：
{
  "overall_score": 75.0,
  "overall_level": "B",
  "dimension_score": {
    "教育理念与师德": 80.0,
    "学科素养": 75.0,
    "教学设计": 70.0,
    "课堂管理与应变": 65.0,
    "沟通表达与反思": 80.0
  },
  "strengths": ["有作答证据支撑的优势"],
  "weaknesses": ["可通过训练改善的具体能力"],
  "detailed_review": [
    {
      "question_content": "题目内容",
      "user_answer": "学员回答摘要",
      "score": 75.0,
      "comment": "基于证据的点评与下一步建议",
      "key_points_hit": ["已体现的能力点"],
      "key_points_missed": ["待补充的能力点"]
    }
  ],
  "summary": "综合诊断与下一步训练建议"
}

等级仅表示本次训练表现：A 为能够迁移到复杂情境；B 为主要能力良好；C 为具备基础但需系统训练；D 为当前证据不足、需从基础训练。`

// Evaluator 评估 Agent，负责生成教学能力训练评估报告
type Evaluator struct {
	chatModel model.ChatModel
}

// NewEvaluator 创建评估 Agent
func NewEvaluator(chatModel model.ChatModel) *Evaluator {
	return &Evaluator{chatModel: chatModel}
}

// Evaluate 生成训练评估报告。userTerminated 表示训练是否由用户主动终止。
func (e *Evaluator) Evaluate(ctx context.Context, state *imodel.InterviewState, position string, candidateName string, userTerminated bool) (*imodel.EvaluationReport, error) {
	// 构建训练过程摘要
	var qaText string
	for i, qa := range state.QAHistory {
		qaText += fmt.Sprintf("### 第 %d 题（%s / %s）\n", i+1, qa.Question.Type, qa.Question.Difficulty)
		qaText += fmt.Sprintf("**题目**：%s\n", qa.Question.Content)
		qaText += fmt.Sprintf("**回答**：%s\n", qa.UserAnswer)
		qaText += fmt.Sprintf("**即时得分**：%.0f\n\n", qa.Score)
	}

	terminatedNote := ""
	if userTerminated {
		terminatedNote = fmt.Sprintf("\n\n> **注意：本次训练由学员主动终止。原计划 %d 道题，实际完成 %d 道题。请在综合评语中说明训练未完成，诊断仅基于已作答内容。**\n",
			state.TotalQuestions, len(state.QAHistory))
	}

	userMsg := fmt.Sprintf("## 训练信息\n- 目标教师岗位或考核：%s\n- 学员：%s\n- 计划题目数：%d\n- 实际完成：%d\n- 训练状态：%s%s\n\n## 训练过程\n\n%s",
		position, candidateName, state.TotalQuestions, len(state.QAHistory),
		func() string {
			if userTerminated {
				return "学员主动终止"
			}
			return "正常完成"
		}(), terminatedNote, qaText)

	messages := []*schema.Message{
		schema.SystemMessage(teacherEvaluatorPrompt),
		schema.UserMessage(userMsg),
	}

	resp, err := e.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("evaluator: generate: %w", err)
	}

	result := &imodel.EvaluationReport{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("evaluator: parse response: %w\nraw: %s", err, resp.Content)
	}

	result.SessionID = state.SessionID
	result.CandidateName = candidateName
	result.Position = position
	result.TrainingMetrics = calculateTrainingMetrics(state)
	result.CreatedAt = time.Now()

	return result, nil
}

// FormatReport 将评估报告格式化为 Markdown
func FormatReport(report *imodel.EvaluationReport) string {
	md := fmt.Sprintf("# 教学能力训练评估报告\n\n")
	md += fmt.Sprintf("- **学员**：%s\n", report.CandidateName)
	md += fmt.Sprintf("- **目标教师岗位或考核**：%s\n", report.Position)
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
			{"question_bank_hit_rate", "教师题库命中率", "%"},
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
	for dim, score := range report.DimensionScore {
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

func calculateTrainingMetrics(state *imodel.InterviewState) map[string]float64 {
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
