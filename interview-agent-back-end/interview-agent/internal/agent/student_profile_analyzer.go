/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const studentProfileAnalyzerPrompt = `你是一名学生能力发展诊断专家。请将学生画像与学习目标、课程标准或能力标准进行诊断分析。

诊断用于建立训练起点：识别已有能力证据、待验证能力和后续训练重点。

请按照以下 JSON 格式输出匹配结果（不要输出其他内容，只输出纯 JSON）：

{
  "overall_score": 75.0,
  "ability_assessments": [
    {
      "ability_name": "能力名称",
      "target": true,
      "demonstrated": true,
      "score": 80.0,
      "evidence": "从学生画像中找到的事实证据"
    }
  ],
  "ability_scores": {"能力名称": 80.0},
  "strengths": ["优势1", "优势2"],
  "weaknesses": ["待提升能力1", "待提升能力2"],
  "focus_areas": ["需要重点训练或验证的方向1", "需要重点训练或验证的方向2"],
  "evidence_gaps": ["学生画像中缺少证据、需要通过训练验证的能力1"]
}

评分标准：
- overall_score: 0-100 分，仅表示当前学生画像与能力标准的证据覆盖度
- ability_assessments: 逐项分析知识掌握、理解应用、问题解决、沟通表达和反思迁移等能力
- ability_scores: 按能力名称给出当前证据分数
- strengths: 已有事实能够支撑的能力优势
- weaknesses: 证据不足或需要训练的能力，不对人格作判断
- focus_areas: 后续训练应重点覆盖的方向
- evidence_gaps: 学生画像中需要通过问答、练习或情境任务进一步验证的部分`

// StudentProfileAnalyzer 根据能力标准诊断学生画像。
type StudentProfileAnalyzer struct {
	chatModel model.ChatModel
}

// NewStudentProfileAnalyzer 创建学生画像分析 Agent。
func NewStudentProfileAnalyzer(chatModel model.ChatModel) *StudentProfileAnalyzer {
	return &StudentProfileAnalyzer{chatModel: chatModel}
}

// Analyze 根据能力标准分析学生画像并形成学习诊断。
func (a *StudentProfileAnalyzer) Analyze(ctx context.Context, standard *imodel.AbilityStandard, profile *imodel.StudentProfile) (*imodel.LearningDiagnosis, error) {
	standardSummary := formatAbilityStandardForDiagnosis(standard)
	profileSummary := formatStudentProfileForDiagnosis(profile)

	messages := []*schema.Message{
		schema.SystemMessage(studentProfileAnalyzerPrompt),
		schema.UserMessage(fmt.Sprintf("## 学习目标与能力标准\n\n%s\n\n## 学生画像\n\n%s", standardSummary, profileSummary)),
	}

	resp, err := a.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("student_profile_analyzer: generate: %w", err)
	}

	result := &imodel.LearningDiagnosis{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("student_profile_analyzer: parse response: %w\nraw: %s", err, resp.Content)
	}
	result.StudentID = profile.StudentID
	result.LearningGoal = standard.LearningGoal

	return result, nil
}

// formatAbilityStandardForDiagnosis 将能力标准格式化为诊断上下文。
func formatAbilityStandardForDiagnosis(standard *imodel.AbilityStandard) string {
	data, _ := json.MarshalIndent(standard, "", "  ")
	return string(data)
}

// formatStudentProfileForDiagnosis 将学生画像格式化为诊断上下文。
func formatStudentProfileForDiagnosis(profile *imodel.StudentProfile) string {
	data, _ := json.MarshalIndent(profile, "", "  ")
	return string(data)
}
