package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

const (
	agentTraceStoragePrefix = "agent_trace:"
	agentTraceTTL           = 30 * 24 * time.Hour
	maxTraceSummaryRunes    = 160
)

var (
	ErrAgentTraceNotFound = errors.New("agent trace not found")
	emailPattern          = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern          = regexp.MustCompile(`(?:\+?86[- ]?)?1[3-9]\d{9}`)
)

// AgentTraceService 通过项目现有 Store 保存业务 Trace，不引入外部链路系统或数据库迁移。
type AgentTraceService struct {
	store memory.Store
	mu    sync.Mutex
}

func NewAgentTraceService(store memory.Store) *AgentTraceService {
	if store == nil {
		store = memory.NewMemoryStore()
	}
	return &AgentTraceService{store: store}
}

// Create 创建一次训练 Trace。intent 必须是归一化目标，而不是完整用户输入。
func (s *AgentTraceService) Create(ctx context.Context, sessionID, studentID, intent string) (*imodel.AgentTrace, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(studentID) == "" {
		return nil, fmt.Errorf("agent trace: session_id and student_id are required")
	}
	now := time.Now()
	trace := &imodel.AgentTrace{
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		StudentID:     studentID,
		Intent:        sanitizeTraceText(intent),
		ToolCalls:     []imodel.ToolTrace{},
		MemorySummary: []string{},
		AbilityBefore: map[string]float64{},
		AbilityAfter:  map[string]float64{},
		Status:        imodel.AgentTraceStatusRunning,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.save(ctx, trace); err != nil {
		return nil, err
	}
	return trace, nil
}

func (s *AgentTraceService) UpdateIntent(ctx context.Context, sessionID, intent string) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		trace.Intent = sanitizeTraceText(intent)
	})
}

func (s *AgentTraceService) UpdateSkill(ctx context.Context, sessionID, skillName, reason string) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		trace.SelectedSkill = sanitizeTraceText(skillName)
		trace.DecisionReason = sanitizeTraceText(reason)
	})
}

func (s *AgentTraceService) RecordToolCall(ctx context.Context, sessionID string, call imodel.ToolTrace) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		if call.DurationMs < 0 {
			call.DurationMs = 0
		}
		call.Name = sanitizeTraceText(call.Name)
		call.Summary = sanitizeTraceText(call.Summary)
		trace.ToolCalls = append(trace.ToolCalls, call)
	})
}

func (s *AgentTraceService) RecordMemorySummary(ctx context.Context, sessionID string, summaries ...string) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		seen := make(map[string]bool, len(trace.MemorySummary)+len(summaries))
		for _, summary := range trace.MemorySummary {
			seen[summary] = true
		}
		for _, raw := range summaries {
			summary := sanitizeTraceText(raw)
			if summary == "" || seen[summary] {
				continue
			}
			seen[summary] = true
			trace.MemorySummary = append(trace.MemorySummary, summary)
		}
	})
}

// BindTrainingAttempt 绑定首个主训练事实；后续追问与评价可由该事实链继续追溯。
func (s *AgentTraceService) BindTrainingAttempt(ctx context.Context, sessionID, attemptID string) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		if trace.TrainingAttemptID == "" {
			trace.TrainingAttemptID = sanitizeTraceText(attemptID)
		}
	})
}

func (s *AgentTraceService) SaveAbilityChanges(ctx context.Context, sessionID string, before, after map[string]float64) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		trace.AbilityBefore = cloneTraceScores(before)
		trace.AbilityAfter = cloneTraceScores(after)
		trace.Status = imodel.AgentTraceStatusCompleted
	})
}

func (s *AgentTraceService) UpdateStatus(ctx context.Context, sessionID, status string) error {
	return s.update(ctx, sessionID, func(trace *imodel.AgentTrace) {
		trace.Status = sanitizeTraceText(status)
	})
}

func (s *AgentTraceService) GetBySessionID(ctx context.Context, sessionID string) (*imodel.AgentTrace, error) {
	if s == nil || s.store == nil {
		return nil, ErrAgentTraceNotFound
	}
	data, err := s.store.LoadSession(ctx, agentTraceStoragePrefix+sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent trace: load: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrAgentTraceNotFound
	}
	trace := &imodel.AgentTrace{}
	if err := json.Unmarshal(data, trace); err != nil {
		return nil, fmt.Errorf("agent trace: decode: %w", err)
	}
	return trace, nil
}

func (s *AgentTraceService) update(ctx context.Context, sessionID string, mutate func(*imodel.AgentTrace)) error {
	if s == nil {
		return ErrAgentTraceNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trace, err := s.GetBySessionID(ctx, sessionID)
	if err != nil {
		return err
	}
	mutate(trace)
	trace.UpdatedAt = time.Now()
	return s.save(ctx, trace)
}

func (s *AgentTraceService) save(ctx context.Context, trace *imodel.AgentTrace) error {
	data, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("agent trace: encode: %w", err)
	}
	if err := s.store.SaveSession(ctx, agentTraceStoragePrefix+trace.SessionID, data, agentTraceTTL); err != nil {
		return fmt.Errorf("agent trace: save: %w", err)
	}
	return nil
}

func sanitizeTraceText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = emailPattern.ReplaceAllString(value, "[redacted-email]")
	value = phonePattern.ReplaceAllString(value, "[redacted-phone]")
	if utf8.RuneCountInString(value) <= maxTraceSummaryRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxTraceSummaryRunes]) + "…"
}

func cloneTraceScores(scores map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(scores))
	for ability, score := range scores {
		result[ability] = score
	}
	return result
}
