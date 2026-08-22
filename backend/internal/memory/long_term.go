/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	imodel "interview-agent/internal/model"
)

const (
	weakPointMaxAge = 30 * 24 * time.Hour // 薄弱点超过 30 天自动淘汰
	weakPointTopN   = 10                  // 出题时只取最弱的 Top N
)

// UserProfile 用户画像（长期记忆）
type UserProfile struct {
	UserID        string            `json:"user_id"`
	Name          string            `json:"name"`
	SkillLevel    map[string]string `json:"skill_level"`    // 技能 -> 水平（beginner/intermediate/advanced）
	WeakPoints    []WeakPoint       `json:"weak_points"`    // 薄弱点列表
	InterviewHist []InterviewRecord `json:"interview_hist"` // 训练历史；字段名保留兼容
	UpdatedAt     time.Time         `json:"updated_at"`
}

// WeakPoint 薄弱知识点
type WeakPoint struct {
	Topic      string    `json:"topic"`
	Score      float64   `json:"score"`       // 最近一次得分
	HitCount   int       `json:"hit_count"`   // 被考察次数
	WrongCount int       `json:"wrong_count"` // 答错次数
	LastSeen   time.Time `json:"last_seen"`
}

// InterviewRecord 训练记录摘要；类型名保留历史兼容。
type InterviewRecord struct {
	SessionID    string    `json:"session_id"`
	LearningGoal string    `json:"learning_goal"`
	OverallScore float64   `json:"overall_score"`
	Date         time.Time `json:"date"`
}

// LongTermMemory 长期记忆：管理知识掌握画像和训练历史。
// 当前使用内存存储，后续可替换为 MySQL 持久化
type LongTermMemory struct {
	mu              sync.RWMutex
	profiles        map[string]*UserProfile                  // userID -> profile
	abilityProfiles map[string]*imodel.StudentAbilityProfile // studentID -> ability profile
	store           Store                                    // 可选的持久化存储
}

// NewLongTermMemory 创建长期记忆
func NewLongTermMemory(store Store) *LongTermMemory {
	return &LongTermMemory{
		profiles:        make(map[string]*UserProfile),
		abilityProfiles: make(map[string]*imodel.StudentAbilityProfile),
		store:           store,
	}
}

// GetAbilityProfile 获取跨训练维护的知识掌握画像；无历史时返回 nil。
func (m *LongTermMemory) GetAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error) {
	m.mu.RLock()
	profile := m.abilityProfiles[studentID]
	m.mu.RUnlock()
	if profile != nil {
		return profile, nil
	}
	if m.store == nil {
		return nil, nil
	}
	profile, err := m.store.LoadAbilityProfile(ctx, studentID)
	if err != nil || profile == nil {
		return profile, err
	}
	m.mu.Lock()
	m.abilityProfiles[studentID] = profile
	m.mu.Unlock()
	return profile, nil
}

// SaveAbilityProfile 更新缓存并持久化完整知识掌握画像。
func (m *LongTermMemory) SaveAbilityProfile(ctx context.Context, profile *imodel.StudentAbilityProfile) error {
	m.mu.Lock()
	m.abilityProfiles[profile.StudentID] = profile
	m.mu.Unlock()
	if m.store != nil {
		return m.store.SaveAbilityProfile(ctx, profile)
	}
	return nil
}

// GetProfile 获取用户画像
func (m *LongTermMemory) GetProfile(ctx context.Context, userID string) (*UserProfile, error) {
	m.mu.RLock()
	profile, ok := m.profiles[userID]
	m.mu.RUnlock()

	if ok {
		return profile, nil
	}

	// 尝试从持久化存储加载
	if m.store != nil {
		profile, err := m.store.LoadProfile(ctx, userID)
		if err != nil {
			return nil, err
		}
		if profile != nil {
			m.mu.Lock()
			m.profiles[userID] = profile
			m.mu.Unlock()
			return profile, nil
		}
	}

	// 创建新用户画像
	profile = &UserProfile{
		UserID:     userID,
		SkillLevel: make(map[string]string),
		UpdatedAt:  time.Now(),
	}
	m.mu.Lock()
	m.profiles[userID] = profile
	m.mu.Unlock()

	return profile, nil
}

// UpdateWeakPoints 更新薄弱点
func (m *LongTermMemory) UpdateWeakPoints(ctx context.Context, userID string, topic string, score float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, ok := m.profiles[userID]
	if !ok {
		profile = &UserProfile{
			UserID:     userID,
			SkillLevel: make(map[string]string),
		}
		m.profiles[userID] = profile
	}

	// 查找已有薄弱点
	found := false
	for i := range profile.WeakPoints {
		if profile.WeakPoints[i].Topic == topic {
			profile.WeakPoints[i].Score = score
			profile.WeakPoints[i].HitCount++
			if score < 60 {
				profile.WeakPoints[i].WrongCount++
			}
			profile.WeakPoints[i].LastSeen = time.Now()
			found = true

			// 得分 >= 80 说明已掌握，移除薄弱点
			if score >= 80 {
				profile.WeakPoints = append(profile.WeakPoints[:i], profile.WeakPoints[i+1:]...)
			}
			break
		}
	}

	// 得分 < 60 才记录为薄弱点
	if !found && score < 60 {
		profile.WeakPoints = append(profile.WeakPoints, WeakPoint{
			Topic:      topic,
			Score:      score,
			HitCount:   1,
			WrongCount: 1,
			LastSeen:   time.Now(),
		})
	}

	profile.UpdatedAt = time.Now()

	// 持久化
	if m.store != nil {
		return m.store.SaveProfile(ctx, profile)
	}

	return nil
}

// AddInterviewRecord 添加训练记录；函数名保留历史兼容。
func (m *LongTermMemory) AddInterviewRecord(ctx context.Context, userID string, record InterviewRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, ok := m.profiles[userID]
	if !ok {
		profile = &UserProfile{
			UserID:     userID,
			SkillLevel: make(map[string]string),
		}
		m.profiles[userID] = profile
	}

	profile.InterviewHist = append(profile.InterviewHist, record)
	profile.UpdatedAt = time.Now()

	if m.store != nil {
		return m.store.SaveProfile(ctx, profile)
	}

	return nil
}

// GetWeakPoints 获取用户的薄弱点（淘汰过期 + 按分数排序 + Top N）
func (m *LongTermMemory) GetWeakPoints(ctx context.Context, userID string) []WeakPoint {
	profile, err := m.GetProfile(ctx, userID)
	if err != nil || profile == nil {
		return nil
	}

	now := time.Now()

	// 过滤掉超过 30 天的过期薄弱点
	var active []WeakPoint
	for _, wp := range profile.WeakPoints {
		if now.Sub(wp.LastSeen) <= weakPointMaxAge {
			active = append(active, wp)
		}
	}

	// 按分数升序排序（分数越低越弱，优先返回）
	sort.Slice(active, func(i, j int) bool {
		return active[i].Score < active[j].Score
	})

	// 只返回 Top N
	if len(active) > weakPointTopN {
		active = active[:weakPointTopN]
	}

	return active
}
