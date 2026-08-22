package model

import "time"

const (
	AgentTraceStatusRunning    = "running"
	AgentTraceStatusCompleted  = "completed"
	AgentTraceStatusTerminated = "terminated"
	AgentTraceStatusFailed     = "failed"
)

// AgentTrace 描述一次学生训练中的业务决策与事实关联。
// Trace 只保存可审计摘要，不保存完整 prompt、学生回答、token 或个人敏感信息。
type AgentTrace struct {
	ID string `json:"id"`

	SessionID string `json:"session_id"`
	StudentID string `json:"student_id"`

	Intent string `json:"intent"`

	SelectedSkill  string `json:"selected_skill,omitempty"`
	DecisionReason string `json:"decision_reason,omitempty"`

	ToolCalls []ToolTrace `json:"tool_calls,omitempty"`

	MemorySummary []string `json:"memory_summary,omitempty"`

	TrainingAttemptID string `json:"training_attempt_id,omitempty"`

	AbilityBefore map[string]float64 `json:"ability_before,omitempty"`
	AbilityAfter  map[string]float64 `json:"ability_after,omitempty"`

	Status string `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToolTrace 是一次业务 Tool 调用的安全摘要。
type ToolTrace struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	DurationMs int64  `json:"duration_ms"`
	Summary    string `json:"summary,omitempty"`
}
