package model

import "time"

const (
	AbilityTrendUp     = "up"
	AbilityTrendDown   = "down"
	AbilityTrendStable = "stable"
)

// StudentGrowthDashboard 是面向学生展示的学习轨迹视图；类型名为历史兼容名称。
// 它只聚合已经保存的知识掌握画像、训练、评价和 Trace 事实，不参与掌握度计算。
type StudentGrowthDashboard struct {
	StudentID string `json:"student_id"`

	Abilities map[string]AbilitySnapshot `json:"abilities"`

	RecentTrainings []TrainingSummary `json:"recent_trainings"`

	Strengths  []string `json:"strengths"`
	Weaknesses []string `json:"weaknesses"`

	GrowthTrend []GrowthPoint `json:"growth_trend"`

	NextRecommendations []string `json:"next_recommendations"`
}

// AbilitySnapshot 描述一个能力维度的当前分数及最近一次已记录变化。
type AbilitySnapshot struct {
	Score        float64  `json:"score"`
	Trend        string   `json:"trend"`
	RecentChange float64  `json:"recent_change"`
	Evidence     []string `json:"evidence"`
}

// TrainingSummary 是一次已完成训练的学生侧摘要。
type TrainingSummary struct {
	SessionID         string    `json:"session_id"`
	TrainingAttemptID string    `json:"training_attempt_id,omitempty"`
	Skill             string    `json:"skill,omitempty"`
	Result            string    `json:"result"`
	LearningGoal      string    `json:"learning_goal,omitempty"`
	OverallScore      float64   `json:"overall_score"`
	DecisionReason    string    `json:"decision_reason,omitempty"`
	TrainedAt         time.Time `json:"trained_at"`
}

// GrowthPoint 是学习轨迹中某一评价维度在一次训练后的持久化分数。
type GrowthPoint struct {
	SessionID  string    `json:"session_id"`
	Ability    string    `json:"ability"`
	Score      float64   `json:"score"`
	Change     float64   `json:"change"`
	RecordedAt time.Time `json:"recorded_at"`
}
