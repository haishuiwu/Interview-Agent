/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package agent 实现学生能力提升系统的各个 Agent。
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
1. target_abilities 只记录原文明确要求的知识、应用、问题解决、沟通或反思能力
2. extension_abilities 记录原文中的进阶能力
3. proficiency_level 依据目标难度判断；未说明时使用 foundation
4. key_topics 必须是可以通过问答、练习或情境任务训练诊断的重点
5. 不得补写原文未出现的经历、证书、成果或能力证据`

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
