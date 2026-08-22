/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Mastery Diagnosis
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

const studentProfileAnalyzerPrompt = `你是一名学习状态诊断专家。请将学生已有学习记录与本轮学科、教材、章节和知识点目标进行对齐。

诊断用于建立训练起点：区分“已有直接作答证据”“仅自述经历”和“尚无证据”。不得因年级、学校、项目名称或自我描述就判定已掌握知识点；没有直接证据时必须列为待验证。

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
  "ability_scores": {"兼容能力标签": 80.0},
  "knowledge_mastery": {"具体知识点": 0.0},
  "weak_knowledge_points": ["待验证或已有错误证据的知识点"],
  "strengths": ["优势1", "优势2"],
  "weaknesses": ["待提升能力1", "待提升能力2"],
  "focus_areas": ["需要重点训练或验证的方向1", "需要重点训练或验证的方向2"],
  "evidence_gaps": ["学生画像中缺少证据、需要通过训练验证的能力1"]
}

评分标准：
- overall_score: 0-100 分，仅表示当前学生画像与能力标准的证据覆盖度
- ability_assessments: 逐项分析知识掌握、理解应用、问题解决、沟通表达和反思迁移等能力
- ability_scores: 兼容视图，只汇总有事实支撑的通用能力证据
- knowledge_mastery: 0-100；只有历史作答或练习结果等直接证据时才估计，证据不足时填 0 并列入 weak_knowledge_points
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
