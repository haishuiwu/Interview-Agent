/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package memory 实现 Agent 记忆系统（短期 + 长期）
package memory

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// ShortTermMemory 短期记忆：管理对话上下文的滑动窗口
type ShortTermMemory struct {
	mu       sync.RWMutex
	messages map[string][]*schema.Message // sessionID -> 消息列表
	maxLen   int                          // 最大保留消息数
}

// NewShortTermMemory 创建短期记忆
func NewShortTermMemory(maxLen int) *ShortTermMemory {
	if maxLen <= 0 {
		maxLen = 20
	}
	return &ShortTermMemory{
		messages: make(map[string][]*schema.Message),
		maxLen:   maxLen,
	}
}

// Add 添加消息到会话
func (m *ShortTermMemory) Add(_ context.Context, sessionID string, msg *schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	msgs := m.messages[sessionID]
	msgs = append(msgs, msg)

	// 滑动窗口：保留最近 maxLen 条消息
	if len(msgs) > m.maxLen {
		msgs = msgs[len(msgs)-m.maxLen:]
	}

	m.messages[sessionID] = msgs
}

// AddBatch 批量添加消息
func (m *ShortTermMemory) AddBatch(_ context.Context, sessionID string, msgs []*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.messages[sessionID]
	existing = append(existing, msgs...)

	if len(existing) > m.maxLen {
		existing = existing[len(existing)-m.maxLen:]
	}

	m.messages[sessionID] = existing
}

// Get 获取会话的所有消息
func (m *ShortTermMemory) Get(_ context.Context, sessionID string) []*schema.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs := m.messages[sessionID]
	if len(msgs) == 0 {
		return nil
	}

	// 返回副本
	result := make([]*schema.Message, len(msgs))
	copy(result, msgs)
	return result
}

// GetRecent 获取最近 n 条消息
func (m *ShortTermMemory) GetRecent(_ context.Context, sessionID string, n int) []*schema.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs := m.messages[sessionID]
	if len(msgs) == 0 {
		return nil
	}

	start := 0
	if len(msgs) > n {
		start = len(msgs) - n
	}

	result := make([]*schema.Message, len(msgs)-start)
	copy(result, msgs[start:])
	return result
}

// Clear 清除会话记忆
func (m *ShortTermMemory) Clear(_ context.Context, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, sessionID)
}
