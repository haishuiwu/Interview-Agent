/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Mastery Diagnosis
 */

// Package agent 实现自适应学习训练与知识掌握诊断组件。
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const abilityAnalyzerPrompt = `你是一名学习目标与能力标准分析专家。请分析用户提供的学习目标、课程标准、考试大纲或能力要求，提取可用于训练的能力标准。

你的唯一任务是解析学习目标与能力标准。请按照以下 JSON 格式输出分析结果（不要输出其他内容，只输出纯 JSON）：

{
  "learning_goal": "本轮学习目标",
  "grade": "学段或年级（如有）",
  "subject": "学科或学习领域（如有）",
	"textbook": "教材或课程名称（如有）",
	"chapter": "章节或单元（如有）",
	"knowledge_points": ["原文明确或可直接拆解出的知识点"],
  "standard_source": "课程、考试、培养方案或用户自定义目标（如有）",
  "target_abilities": [
    {"name": "需要掌握的核心能力", "category": "knowledge/application/problem_solving/communication/reflection/other", "importance": "must"}
  ],
  "extension_abilities": [
    {"name": "进阶能力", "category": "knowledge/application/problem_solving/communication/reflection/other", "importance": "preferred"}
  ],
  "proficiency_level": "foundation/intermediate/advanced",
  "learning_requirements": ["学习或实践要求1", "要求2"],
  "key_topics": ["本轮训练需要覆盖的能力方向1", "能力方向2"]
}

注意：
1. knowledge_points 是本轮训练与诊断的主对象，使用可检索、可评价的具体概念或方法名，不得用宽泛能力代替
2. target_abilities 只记录原文明确要求的应用、问题解决、表达或反思等兼容能力标签
3. extension_abilities 记录原文中的进阶能力
4. proficiency_level 依据目标难度判断；未说明时使用 foundation
5. key_topics 围绕知识点的概念理解、条件边界、典型错误和迁移应用
6. 不得补写原文未出现的经历、证书、成果或能力证据`

// AbilityAnalyzer 负责解析学习目标并提取结构化能力标准。
type AbilityAnalyzer struct {
	chatModel model.ChatModel
}

// NewAbilityAnalyzer 创建能力标准分析 Agent。
func NewAbilityAnalyzer(chatModel model.ChatModel) *AbilityAnalyzer {
	return &AbilityAnalyzer{chatModel: chatModel}
}

// Analyze 分析学习目标文本，返回结构化能力标准。
func (a *AbilityAnalyzer) Analyze(ctx context.Context, standardText string) (*imodel.AbilityStandard, error) {
	messages := []*schema.Message{
		schema.SystemMessage(abilityAnalyzerPrompt),
		schema.UserMessage(fmt.Sprintf("请解析以下学习目标与能力标准：\n\n%s", standardText)),
	}

	resp, err := a.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("ability_analyzer: generate: %w", err)
	}

	// 解析 JSON 响应
	result := &imodel.AbilityStandard{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("ability_analyzer: parse response: %w\nraw: %s", err, resp.Content)
	}

	result.RawText = standardText
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
