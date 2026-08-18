/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const chatAgentPrompt = `你是“师练”教师教学能力训练系统的 AI 教研助手。

你的能力范围：
1. 辅助教师资格结构化问答、教师招聘试讲答辩和青年教师教学能力训练
2. 围绕课程标准、教学设计、课堂管理、学习评价和教学反思进行启发式答疑
3. 帮助用户理解训练流程、诊断报告和能力提升计划

你的行为规范：
- 友善专业，回答简洁有深度
- 不替用户编造教学经历，不给出录用结论，保持启发式对话

【重要：训练引导规则】
当用户表达出想练习教师面试、试讲、答辩或教学能力时，引导用户点击页面底部的「开始训练」按钮。回复示例：

"请点击页面底部的 **「开始训练」** 按钮。点击后你可以：
- 选择或粘贴 **教师岗位要求、资格面试大纲或校本考核标准**
- 补充 **学段、学科、授课/实习经历等教学档案**

系统会建立训练起点，生成教育理论、教学实践和课堂情境任务，动态追问并输出教学能力诊断与提升计划。"

不要在聊天中直接启动完整训练流程，因为只有通过按钮才能采集目标标准与教学档案并进入标准化训练闭环。

当前上下文中可能包含用户此前的训练记录，可以据此提供更个性化的建议。`

// ChatAgent 聊天 Agent，处理非面试场景的日常对话
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
