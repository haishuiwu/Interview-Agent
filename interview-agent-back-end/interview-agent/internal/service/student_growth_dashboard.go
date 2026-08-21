package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	imodel "interview-agent/internal/model"
)

const (
	dashboardRecentTrainingLimit = 5
	dashboardGrowthRecordLimit   = 12
	dashboardEvidenceLimit       = 3
)

// DashboardTraceReader 是 Dashboard 使用的 AgentTrace 只读边界。
type DashboardTraceReader interface {
	GetBySessionID(ctx context.Context, sessionID string) (*imodel.AgentTrace, error)
}

// DashboardGrowthReader 是 Dashboard 对 StudentGrowthService 的最小只读依赖。
type DashboardGrowthReader interface {
	GetAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error)
}

type latestTrainingAttemptReader interface {
	GetLatestTrainingAttempts(ctx context.Context, studentID string) ([]*imodel.TrainingAttempt, error)
}

// StudentGrowthDashboardService 聚合已有成长事实，不生成或重算能力分。
type StudentGrowthDashboardService struct {
	growthService DashboardGrowthReader
	traceReader   DashboardTraceReader
}

func NewStudentGrowthDashboardService(
	growthService DashboardGrowthReader,
	traceReader DashboardTraceReader,
) *StudentGrowthDashboardService {
	return &StudentGrowthDashboardService{
		growthService: growthService,
		traceReader:   traceReader,
	}
}

// GetDashboard 返回学生当前画像、训练历史、成长趋势和下一步建议。
func (s *StudentGrowthDashboardService) GetDashboard(
	ctx context.Context,
	studentID string,
) (*imodel.StudentGrowthDashboard, error) {
	studentID = strings.TrimSpace(studentID)
	if studentID == "" {
		return nil, fmt.Errorf("student growth dashboard: student_id is required")
	}
	if s == nil || s.growthService == nil {
		return nil, fmt.Errorf("student growth dashboard: growth service is unavailable")
	}

	profile, err := s.growthService.GetAbilityProfile(ctx, studentID)
	if err != nil {
		return nil, fmt.Errorf("student growth dashboard: load ability profile: %w", err)
	}
	if profile == nil {
		profile = &imodel.StudentAbilityProfile{
			StudentID:     studentID,
			AbilityScores: map[string]float64{},
		}
	}

	attempts, err := s.loadLatestTrainingAttempts(ctx, studentID)
	if err != nil {
		return nil, fmt.Errorf("student growth dashboard: load training attempts: %w", err)
	}
	attemptByID := make(map[string]*imodel.TrainingAttempt, len(attempts))
	for _, attempt := range attempts {
		if attempt != nil && attempt.ID != "" {
			attemptByID[attempt.ID] = attempt
		}
	}

	records := append([]imodel.AbilityGrowthRecord(nil), profile.GrowthHistory...)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].TrainingTime.Before(records[j].TrainingTime)
	})
	latestChanges := latestRecordedChanges(records)
	evidence := dashboardEvidence(attempts, profile.Summary)

	dashboard := &imodel.StudentGrowthDashboard{
		StudentID:           studentID,
		Abilities:           make(map[string]imodel.AbilitySnapshot, len(profile.AbilityScores)),
		RecentTrainings:     []imodel.TrainingSummary{},
		Strengths:           append([]string(nil), profile.Strengths...),
		Weaknesses:          append([]string(nil), profile.Weaknesses...),
		GrowthTrend:         buildGrowthTrend(records),
		NextRecommendations: []string{},
	}

	for ability, score := range profile.AbilityScores {
		change := latestChanges[ability]
		abilityEvidence := evidence[ability]
		if len(abilityEvidence) == 0 {
			abilityEvidence = evidence[""]
		}
		dashboard.Abilities[ability] = imodel.AbilitySnapshot{
			Score:        score,
			Trend:        abilityTrend(change),
			RecentChange: change,
			Evidence:     append([]string(nil), abilityEvidence...),
		}
	}

	recommendations := make([]string, 0, 3)
	for i := len(records) - 1; i >= 0 && len(dashboard.RecentTrainings) < dashboardRecentTrainingLimit; i-- {
		record := records[i]
		trace, traceErr := s.loadTrace(ctx, record.SessionID)
		if traceErr != nil {
			return nil, fmt.Errorf("student growth dashboard: load trace for %s: %w", record.SessionID, traceErr)
		}
		summary := trainingSummary(record, trace, attemptByID)
		dashboard.RecentTrainings = append(dashboard.RecentTrainings, summary)
		if trace != nil {
			recommendations = appendUniqueRecommendation(
				recommendations,
				recommendationForSkill(trace.SelectedSkill),
			)
		}
	}

	if len(recommendations) == 0 {
		for _, weakness := range profile.Weaknesses {
			recommendations = appendUniqueRecommendation(
				recommendations,
				fmt.Sprintf("针对“%s”安排下一轮训练", strings.TrimSpace(weakness)),
			)
			if len(recommendations) == 3 {
				break
			}
		}
	}
	dashboard.NextRecommendations = recommendations
	return dashboard, nil
}

func (s *StudentGrowthDashboardService) loadLatestTrainingAttempts(
	ctx context.Context,
	studentID string,
) ([]*imodel.TrainingAttempt, error) {
	reader, ok := s.growthService.(latestTrainingAttemptReader)
	if !ok {
		return []*imodel.TrainingAttempt{}, nil
	}
	attempts, err := reader.GetLatestTrainingAttempts(ctx, studentID)
	if attempts == nil {
		attempts = []*imodel.TrainingAttempt{}
	}
	return attempts, err
}

func (s *StudentGrowthDashboardService) loadTrace(
	ctx context.Context,
	sessionID string,
) (*imodel.AgentTrace, error) {
	if s.traceReader == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	trace, err := s.traceReader.GetBySessionID(ctx, sessionID)
	if errors.Is(err, ErrAgentTraceNotFound) {
		return nil, nil
	}
	return trace, err
}

func latestRecordedChanges(records []imodel.AbilityGrowthRecord) map[string]float64 {
	changes := make(map[string]float64)
	for _, record := range records {
		for ability, change := range record.ScoreChanges {
			changes[ability] = change
		}
	}
	return changes
}

func abilityTrend(change float64) string {
	switch {
	case change > 0:
		return imodel.AbilityTrendUp
	case change < 0:
		return imodel.AbilityTrendDown
	default:
		return imodel.AbilityTrendStable
	}
}

func buildGrowthTrend(records []imodel.AbilityGrowthRecord) []imodel.GrowthPoint {
	if len(records) > dashboardGrowthRecordLimit {
		records = records[len(records)-dashboardGrowthRecordLimit:]
	}
	points := make([]imodel.GrowthPoint, 0)
	for _, record := range records {
		abilities := make([]string, 0, len(record.AfterScores))
		for ability := range record.AfterScores {
			abilities = append(abilities, ability)
		}
		sort.Strings(abilities)
		for _, ability := range abilities {
			points = append(points, imodel.GrowthPoint{
				SessionID:  record.SessionID,
				Ability:    ability,
				Score:      record.AfterScores[ability],
				Change:     record.ScoreChanges[ability],
				RecordedAt: record.TrainingTime,
			})
		}
	}
	return points
}

func trainingSummary(
	record imodel.AbilityGrowthRecord,
	trace *imodel.AgentTrace,
	attemptByID map[string]*imodel.TrainingAttempt,
) imodel.TrainingSummary {
	summary := imodel.TrainingSummary{
		SessionID:    record.SessionID,
		Result:       "completed",
		LearningGoal: record.LearningGoal,
		OverallScore: record.OverallScore,
		TrainedAt:    record.TrainingTime,
	}
	for _, attemptID := range record.TrainingAttemptIDs {
		if summary.TrainingAttemptID == "" {
			summary.TrainingAttemptID = attemptID
		}
		if attempt := attemptByID[attemptID]; attempt != nil {
			summary.TrainingAttemptID = attempt.ID
			summary.Skill = attempt.SkillName
			break
		}
	}
	if trace != nil {
		if summary.TrainingAttemptID == "" {
			summary.TrainingAttemptID = trace.TrainingAttemptID
		}
		if summary.Skill == "" {
			summary.Skill = trace.SelectedSkill
		}
		summary.DecisionReason = trace.DecisionReason
	}
	if summary.Skill == "" {
		abilities := make([]string, 0, len(record.ScoreChanges))
		for ability := range record.ScoreChanges {
			abilities = append(abilities, ability)
		}
		sort.Strings(abilities)
		if len(abilities) > 0 {
			summary.Skill = imodel.AbilitySkillName(abilities[0])
		}
	}
	return summary
}

func dashboardEvidence(
	attempts []*imodel.TrainingAttempt,
	profileSummary string,
) map[string][]string {
	evidence := make(map[string][]string)
	for _, attempt := range attempts {
		if attempt == nil || attempt.EvaluationResult == nil {
			continue
		}
		summary := summarizeDashboardEvidence(attempt.EvaluationResult.Feedback)
		if summary == "" {
			continue
		}
		abilities := make(map[string]bool)
		for _, criterion := range attempt.Rubric {
			ability := imodel.NormalizeAbilityDimension(criterion.Ability)
			if ability != "" {
				abilities[ability] = true
			}
		}
		for ability := range attempt.AbilityChanges {
			normalized := imodel.NormalizeAbilityDimension(ability)
			if normalized != "" {
				abilities[normalized] = true
			}
		}
		for ability := range abilities {
			evidence[ability] = appendUniqueEvidence(evidence[ability], summary)
		}
	}
	if summary := summarizeDashboardEvidence(profileSummary); summary != "" {
		evidence[""] = []string{summary}
	}
	return evidence
}

func appendUniqueEvidence(items []string, value string) []string {
	if value == "" || len(items) >= dashboardEvidenceLimit {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func summarizeDashboardEvidence(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return value
}

func recommendationForSkill(skill string) string {
	switch strings.TrimSpace(skill) {
	case "communication-training":
		return "继续进行表达训练"
	case "logical-thinking":
		return "继续进行逻辑思维训练"
	case "critical-thinking":
		return "继续进行批判性思维训练"
	case "reflection-training":
		return "继续进行反思训练"
	case "problem-solving":
		return "继续进行问题解决训练"
	case "":
		return ""
	default:
		return fmt.Sprintf("继续进行 %s 训练", strings.TrimSpace(skill))
	}
}

func appendUniqueRecommendation(items []string, value string) []string {
	if value == "" || len(items) >= 3 {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
