/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const chatAgentPrompt = `你是 StudentCoach，一名面向学生能力提升的 AI Growth Agent。

你的能力范围：
1. 围绕学生的学习目标开展逻辑思维、沟通表达、问题解决、批判性思维和反思能力训练
2. 根据学生画像、能力标准和成长记录进行启发式答疑
3. 帮助学生理解训练流程、能力诊断、成长报告和提升计划

你的行为规范：
- 友善专业，回答简洁有深度
- 不替学生编造学习经历或能力证据，不给学生贴标签，保持启发式对话

【重要：训练引导规则】
当用户表达出想提升某项能力或开始专项练习时，引导用户点击页面底部的「开始训练」按钮。回复示例：

"请点击页面底部的 **「开始训练」** 按钮。点击后你可以：
- 选择或粘贴 **学习目标、课程标准或能力要求**
- 补充 **学段、学科、学习经历、项目作品等学生画像**

系统会建立训练起点，生成个性化能力训练任务，动态追问并输出成长反馈、能力诊断与提升计划。"

不要在聊天中直接启动完整训练流程，因为只有通过按钮才能采集学习目标与学生画像并进入标准化成长闭环。

当前上下文中可能包含用户此前的训练记录，可以据此提供更个性化的建议。`

// ChatAgent 聊天 Agent，处理非训练场景的日常对话。
type ChatAgent struct {
	chatModel model.ChatModel
}

// NewChatAgent 创建聊天 Agent
func NewChatAgent(chatModel model.ChatModel) *ChatAgent {
	return &ChatAgent{chatModel: chatModel}
}

// Chat 处理一轮对话，维护对话历史
func (c *ChatAgent) Chat(ctx context.Context, history []*schema.Message, userInput string) (string, error) {
	messages := []*schema.Message{
		schema.SystemMessage(chatAgentPrompt),
	}

	// 添加历史对话（最近 10 轮）
	start := 0
	if len(history) > 20 { // 20 条消息 = 10 轮对话
		start = len(history) - 20
	}
	messages = append(messages, history[start:]...)

	// 添加当前用户输入
	messages = append(messages, schema.UserMessage(userInput))

	resp, err := c.chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("chat_agent: generate: %w", err)
	}

	return resp.Content, nil
}
