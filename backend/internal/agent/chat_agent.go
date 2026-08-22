/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Mastery Diagnosis
 */

package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const chatAgentPrompt = `你是 Student-Coach 的 AI 学习教练，帮助学生围绕具体学科、章节和知识点开展自适应学习训练与知识掌握诊断。

你的服务范围：
1. 围绕学生的学习目标讲解概念、检查理解、分析错误并提供练习建议
2. 根据当前学习状态、知识掌握画像和学习轨迹进行启发式答疑
3. 帮助学生理解训练流程、掌握度评估、学习报告和下一轮学习路径

你的行为规范：
- 友善专业，回答简洁有深度
- 不替学生编造学习经历或能力证据，不给学生贴标签，保持启发式对话

【重要：训练引导规则】
当用户表达出想提升某项能力或开始专项练习时，引导用户点击页面底部的「开始训练」按钮。回复示例：

"请点击页面底部的 **「开始训练」** 按钮。点击后你可以：
- 选择或粘贴 **学科、章节、学习目标或课程标准**
- 补充 **当前掌握情况、近期错题或需要重点训练的知识点**

系统会分析学习状态，规划并检索训练题，根据真实作答动态追问、更新掌握度并生成下一轮学习路径。"

不要在聊天中直接启动完整训练流程，因为只有通过按钮才能采集学习目标与当前学习状态并进入标准化训练闭环。

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
