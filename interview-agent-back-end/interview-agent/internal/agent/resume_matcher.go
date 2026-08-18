/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
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

const resumeMatcherPrompt = `你是一名教师发展诊断专家。请将学员的教学档案与目标教师岗位、教师资格面试大纲或教学能力考核标准进行匹配分析。

这里的匹配不是给出录用结论，而是建立训练起点：识别已有教学证据、待验证能力和后续训练重点。

请按照以下 JSON 格式输出匹配结果（不要输出其他内容，只输出纯 JSON）：

{
  "overall_score": 75.0,
  "skill_match": [
    {
      "skill_name": "教学能力名称",
      "required": true,
      "matched": true,
      "match_score": 80.0,
      "evidence": "从教学档案中找到的事实证据"
    }
  ],
  "strengths": ["优势1", "优势2"],
  "weaknesses": ["薄弱点1", "薄弱点2"],
  "focus_areas": ["需要重点训练或验证的方向1", "需要重点训练或验证的方向2"],
  "resume_gaps": ["教学档案中缺少证据、需要通过训练验证的能力1"]
}

评分标准：
- overall_score: 0-100 分，仅表示当前教学档案与目标标准的证据匹配度，不代表录用概率
- skill_match: 逐项分析学科素养、教学设计、课堂实施、班级管理、沟通反思等要求
- strengths: 已有事实能够支撑的教学优势
- weaknesses: 证据不足或需要训练的能力，不对人格和职业适配作判断
- focus_areas: 后续训练应重点覆盖的方向
- resume_gaps: 教学档案中需要通过情境问答、试讲或答辩进一步验证的部分`

// ResumeMatcher 简历匹配 Agent，负责分析简历与 JD 的匹配度
type ResumeMatcher struct {
	chatModel model.ChatModel
}

// NewResumeMatcher 创建简历匹配 Agent
func NewResumeMatcher(chatModel model.ChatModel) *ResumeMatcher {
	return &ResumeMatcher{chatModel: chatModel}
}

// Match 分析简历与 JD 的匹配度
func (m *ResumeMatcher) Match(ctx context.Context, jdAnalysis *imodel.JDAnalysis, resume *imodel.Resume) (*imodel.ResumeMatchResult, error) {
	// 构造 JD 分析摘要
	jdSummary := formatJDForMatching(jdAnalysis)
	resumeSummary := formatResumeForMatching(resume)

	messages := []*schema.Message{
		schema.SystemMessage(resumeMatcherPrompt),
		schema.UserMessage(fmt.Sprintf("## 目标教师岗位或考核标准\n\n%s\n\n## 学员教学档案\n\n%s", jdSummary, resumeSummary)),
	}

	resp, err := m.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("resume_matcher: generate: %w", err)
	}

	result := &imodel.ResumeMatchResult{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("resume_matcher: parse response: %w\nraw: %s", err, resp.Content)
	}

	return result, nil
}

// formatJDForMatching 将 JD 分析结果格式化为便于匹配的文本
func formatJDForMatching(jd *imodel.JDAnalysis) string {
	data, _ := json.MarshalIndent(jd, "", "  ")
	return string(data)
}

// formatResumeForMatching 将简历格式化为便于匹配的文本
func formatResumeForMatching(resume *imodel.Resume) string {
	if resume.RawText != "" {
		return resume.RawText
	}
	data, _ := json.MarshalIndent(resume, "", "  ")
	return string(data)
}
