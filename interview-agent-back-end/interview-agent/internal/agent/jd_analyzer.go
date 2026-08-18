/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package agent 实现 InterviewAgent 系统的各个 Agent
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const jdAnalyzerPrompt = `你是一名教师招聘与教师发展标准分析专家。请分析用户提供的教师岗位要求、教师资格面试大纲或教学能力考核标准，提取可用于训练的能力要求。

你的唯一任务是解析教师岗位要求、教师资格面试大纲或教学能力考核标准。请按照以下 JSON 格式输出分析结果（不要输出其他内容，只输出纯 JSON）：

{
  "position": "目标教师岗位或考核名称",
  "company": "学校、教育机构或考核组织（如有）",
  "required_skills": [
    {"name": "必须具备的教学能力", "category": "ethics/subject/pedagogy/classroom/assessment/communication/other", "importance": "must"}
  ],
  "preferred_skills": [
    {"name": "进阶或加分教学能力", "category": "ethics/subject/pedagogy/classroom/assessment/communication/other", "importance": "preferred"}
  ],
  "experience_level": "pre_service/novice/experienced",
  "responsibilities": ["教学或育人职责1", "职责2"],
  "key_topics": ["本轮训练需要覆盖的能力方向1", "能力方向2"]
}

注意：
1. required_skills 只记录原文明确要求的学科、教学、育人和沟通能力
2. preferred_skills 记录原文中的进阶要求或加分能力
3. experience_level 依据身份或年限判断；未说明时使用 pre_service
4. key_topics 是可以通过结构化问答、说课试讲或课堂情境训练诊断的重点
5. 不得补写原文未出现的学校、经历、证书或教学成果`

// JDAnalyzer JD 分析 Agent，负责解析岗位描述并提取结构化信息
type JDAnalyzer struct {
	chatModel model.ChatModel
}

// NewJDAnalyzer 创建 JD 分析 Agent
func NewJDAnalyzer(chatModel model.ChatModel) *JDAnalyzer {
	return &JDAnalyzer{chatModel: chatModel}
}

// Analyze 分析 JD 文本，返回结构化的分析结果
func (a *JDAnalyzer) Analyze(ctx context.Context, jdText string) (*imodel.JDAnalysis, error) {
	messages := []*schema.Message{
		schema.SystemMessage(jdAnalyzerPrompt),
		schema.UserMessage(fmt.Sprintf("请解析以下教师岗位要求或教学能力考核标准：\n\n%s", jdText)),
	}

	resp, err := a.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("jd_analyzer: generate: %w", err)
	}

	// 解析 JSON 响应
	result := &imodel.JDAnalysis{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("jd_analyzer: parse response: %w\nraw: %s", err, resp.Content)
	}

	result.RawJD = jdText
	return result, nil
}

// extractJSON 从可能包含 markdown 代码块的文本中提取 JSON
func extractJSON(text string) string {
	// 尝试提取 ```json ... ``` 中的内容
	start := -1
	for i := 0; i < len(text)-2; i++ {
		if text[i] == '{' {
			start = i
			break
		}
	}
	if start == -1 {
		return text
	}

	// 找到最后一个 }
	end := -1
	for i := len(text) - 1; i >= start; i-- {
		if text[i] == '}' {
			end = i + 1
			break
		}
	}
	if end == -1 {
		return text
	}

	return text[start:end]
}
