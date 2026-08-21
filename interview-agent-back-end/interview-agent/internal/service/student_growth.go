// Package service 包含由 Go 控制的学生成长数据与业务服务。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/rag"
)

// StudentProfileSnapshot 是 Tool 可读取的学生成长画像视图。
type StudentProfileSnapshot struct {
	StudentID     string            `json:"student_id"`
	Name          string            `json:"name,omitempty"`
	AbilityLevels map[string]string `json:"ability_levels,omitempty"`
	Weaknesses    []AbilityWeakness `json:"weaknesses,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at,omitempty"`
}

// AbilityWeakness 是已有训练记录中的能力短板。
type AbilityWeakness struct {
	Ability  string    `json:"ability"`
	Score    float64   `json:"score"`
	Attempts int       `json:"attempts"`
	LastSeen time.Time `json:"last_seen"`
}

// GrowthHistoryItem 是一轮历史训练摘要。
type GrowthHistoryItem struct {
	SessionID    string    `json:"session_id"`
	LearningGoal string    `json:"learning_goal"`
	OverallScore float64   `json:"overall_score"`
	Date         time.Time `json:"date"`
}

// AbilityReportSnapshot 是最近一次评价报告的数据视图。
type AbilityReportSnapshot struct {
	SessionID     string             `json:"session_id"`
	StudentID     string             `json:"student_id"`
	LearningGoal  string             `json:"learning_goal"`
	OverallScore  float64            `json:"overall_score"`
	AbilityScores map[string]float64 `json:"ability_scores,omitempty"`
	Strengths     []string           `json:"strengths,omitempty"`
	Weaknesses    []string           `json:"weaknesses,omitempty"`
	Summary       string             `json:"summary,omitempty"`
	CreatedAt     time.Time          `json:"created_at,omitempty"`
}

// TrainingCase 是 RAG 返回的学生训练案例。
type TrainingCase struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Reference  string   `json:"reference,omitempty"`
	Abilities  []string `json:"abilities,omitempty"`
	Difficulty string   `json:"difficulty,omitempty"`
}

// TrainingTaskRecommendation 是 Go Service 形成的训练任务建议。
type TrainingTaskRecommendation struct {
	SkillName    string `json:"skill_name"`
	Ability      string `json:"ability"`
	Objective    string `json:"objective"`
	TrainingTask string `json:"training_task"`
	Reason       string `json:"reason"`
}

// GrowthRecordInput 是保存一轮成长结果所需的数据。
type GrowthRecordInput struct {
	SessionID        string                    `json:"session_id,omitempty"`
	TrainingAttempts []*imodel.TrainingAttempt `json:"training_attempts,omitempty"`
	LearningGoal     string                    `json:"learning_goal"`
	OverallScore     float64                   `json:"overall_score"`
	AbilityScores    map[string]float64        `json:"ability_scores,omitempty"`
	Strengths        []string                  `json:"strengths,omitempty"`
	Weaknesses       []string                  `json:"weaknesses,omitempty"`
	Summary          string                    `json:"summary,omitempty"`
	TrainingTime     time.Time                 `json:"training_time,omitempty"`
}

// SavedGrowthRecord 是保存结果的确认信息。
type SavedGrowthRecord struct {
	StudentID string    `json:"student_id"`
	SessionID string    `json:"session_id"`
	SavedAt   time.Time `json:"saved_at"`
}

// AbilityProfileUpdateResult 返回更新后的长期画像和对应成长记录。
type AbilityProfileUpdateResult struct {
	Profile      *imodel.StudentAbilityProfile `json:"profile"`
	GrowthRecord *SavedGrowthRecord            `json:"growth_record"`
}

// StudentGrowthService 定义教育 Tool 所需的数据能力。
type StudentGrowthService interface {
	GetStudentProfile(ctx context.Context, studentID string) (*StudentProfileSnapshot, error)
	GetAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error)
	UpdateAbilityProfile(ctx context.Context, studentID string, input GrowthRecordInput) (*AbilityProfileUpdateResult, error)
	GetGrowthHistory(ctx context.Context, studentID, ability string, limit int) ([]GrowthHistoryItem, error)
	GetAbilityReport(ctx context.Context, studentID string) (*AbilityReportSnapshot, error)
	SearchTrainingCases(ctx context.Context, studentID, abilityGap string, limit int) ([]TrainingCase, error)
	RecommendTrainingTask(ctx context.Context, studentID, learningGoal, ability, caseContent string) (*TrainingTaskRecommendation, error)
	SaveGrowthRecord(ctx context.Context, studentID string, input GrowthRecordInput) (*SavedGrowthRecord, error)
}

// StudentGrowthDataService 使用现有 Memory、评价 JSON 与 RAG 提供学生成长数据。
type StudentGrowthDataService struct {
	store       memory.Store
	mysqlStore  *memory.MySQLStore
	milvusStore *rag.MilvusStore
	bm25Manager *rag.BM25Manager
}

// NewStudentGrowthDataService 创建学生成长数据服务，不创建或修改数据库结构。
func NewStudentGrowthDataService(
	store memory.Store,
	mysqlStore *memory.MySQLStore,
	milvusStore *rag.MilvusStore,
	bm25Manager *rag.BM25Manager,
) *StudentGrowthDataService {
	return &StudentGrowthDataService{
		store:       store,
		mysqlStore:  mysqlStore,
		milvusStore: milvusStore,
		bm25Manager: bm25Manager,
	}
}

func (s *StudentGrowthDataService) GetStudentProfile(ctx context.Context, studentID string) (*StudentProfileSnapshot, error) {
	result := &StudentProfileSnapshot{StudentID: studentID, AbilityLevels: map[string]string{}}
	if s.store == nil {
		return result, nil
	}
	profile, err := s.store.LoadProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return result, nil
	}
	result.Name = profile.Name
	result.AbilityLevels = profile.SkillLevel
	result.UpdatedAt = profile.UpdatedAt
	for _, weakness := range profile.WeakPoints {
		result.Weaknesses = append(result.Weaknesses, AbilityWeakness{
			Ability:  weakness.Topic,
			Score:    weakness.Score,
			Attempts: weakness.HitCount,
			LastSeen: weakness.LastSeen,
		})
	}
	return result, nil
}

// GetAbilityProfile 读取长期能力画像；首次训练时返回空画像而不是虚构分数。
func (s *StudentGrowthDataService) GetAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error) {
	if s.store == nil {
		return &imodel.StudentAbilityProfile{StudentID: studentID, AbilityScores: map[string]float64{}}, nil
	}
	profile, err := s.store.LoadAbilityProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		profile = &imodel.StudentAbilityProfile{StudentID: studentID, AbilityScores: map[string]float64{}}
	}
	if profile.StudentID == "" {
		profile.StudentID = studentID
	}
	if profile.AbilityScores == nil {
		profile.AbilityScores = map[string]float64{}
	}
	return profile, nil
}

// UpdateAbilityProfile 由 Go 聚合本轮评分与历史分数，并在画像更新后保存成长记录。
// 本轮能力分与历史能力分各占 50%；首次出现的维度直接采用本轮分数。
func (s *StudentGrowthDataService) UpdateAbilityProfile(ctx context.Context, studentID string, input GrowthRecordInput) (*AbilityProfileUpdateResult, error) {
	profile, err := s.GetAbilityProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}
	trainingTime := input.TrainingTime
	if trainingTime.IsZero() {
		trainingTime = time.Now()
	}
	if input.SessionID == "" {
		input.SessionID = fmt.Sprintf("growth_%d", trainingTime.UnixNano())
	}
	input.TrainingTime = trainingTime

	before := cloneScores(profile.AbilityScores)
	changes := make(map[string]float64)
	for rawAbility, rawScore := range input.AbilityScores {
		ability := imodel.NormalizeAbilityDimension(rawAbility)
		if ability == "" {
			continue
		}
		evidenceScore := normalizeAbilityScore(rawScore)
		previous, exists := profile.AbilityScores[ability]
		updated := evidenceScore
		if exists {
			updated = (previous + evidenceScore) / 2
		}
		updated = roundAbilityScore(updated)
		profile.AbilityScores[ability] = updated
		changes[ability] = roundAbilityScore(updated - previous)
	}

	profile.StudentID = studentID
	if input.Summary != "" {
		profile.Summary = input.Summary
	}
	profile.Strengths = mergeProfileStatements(profile.Strengths, input.Strengths, 12)
	profile.Weaknesses = mergeProfileStatements(profile.Weaknesses, input.Weaknesses, 12)
	profile.LastTrainingTime = trainingTime
	profile.GrowthHistory = append(profile.GrowthHistory, imodel.AbilityGrowthRecord{
		SessionID:          input.SessionID,
		TrainingAttemptIDs: trainingAttemptIDs(input.TrainingAttempts),
		LearningGoal:       input.LearningGoal,
		BeforeScores:       before,
		AfterScores:        cloneScores(profile.AbilityScores),
		ScoreChanges:       changes,
		OverallScore:       input.OverallScore,
		TrainingTime:       trainingTime,
	})
	if len(profile.GrowthHistory) > 50 {
		profile.GrowthHistory = profile.GrowthHistory[len(profile.GrowthHistory)-50:]
	}

	if s.store != nil {
		if err := s.store.SaveAbilityProfile(ctx, profile); err != nil {
			return nil, err
		}
	}
	record, err := s.SaveGrowthRecord(ctx, studentID, input)
	if err != nil {
		return nil, err
	}
	return &AbilityProfileUpdateResult{Profile: profile, GrowthRecord: record}, nil
}

func (s *StudentGrowthDataService) GetGrowthHistory(ctx context.Context, studentID, ability string, limit int) ([]GrowthHistoryItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	if s.store == nil {
		return []GrowthHistoryItem{}, nil
	}
	targetAbility := imodel.NormalizeAbilityDimension(ability)
	items := make([]GrowthHistoryItem, 0)
	seenSessions := make(map[string]bool)

	abilityProfile, err := s.GetAbilityProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}
	for _, record := range abilityProfile.GrowthHistory {
		if targetAbility != "" {
			if _, changed := record.ScoreChanges[targetAbility]; !changed {
				continue
			}
		}
		seenSessions[record.SessionID] = true
		items = append(items, GrowthHistoryItem{
			SessionID:    record.SessionID,
			LearningGoal: record.LearningGoal,
			OverallScore: record.OverallScore,
			Date:         record.TrainingTime,
		})
	}

	legacyProfile, err := s.store.LoadProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}
	rawAbility := strings.TrimSpace(strings.ToLower(ability))
	if legacyProfile != nil {
		for _, record := range legacyProfile.InterviewHist {
			if seenSessions[record.SessionID] {
				continue
			}
			if targetAbility != "" && imodel.NormalizeAbilityDimension(record.LearningGoal) != targetAbility && !strings.Contains(strings.ToLower(record.LearningGoal), rawAbility) {
				continue
			}
			items = append(items, GrowthHistoryItem{
				SessionID:    record.SessionID,
				LearningGoal: record.LearningGoal,
				OverallScore: record.OverallScore,
				Date:         record.Date,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Date.After(items[j].Date) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *StudentGrowthDataService) GetAbilityReport(ctx context.Context, studentID string) (*AbilityReportSnapshot, error) {
	if s.mysqlStore == nil {
		return nil, nil
	}
	report, err := s.mysqlStore.LoadLatestEvaluationReport(ctx, studentID)
	if err != nil || report == nil {
		return nil, err
	}
	return &AbilityReportSnapshot{
		SessionID:     report.SessionID,
		StudentID:     report.StudentID,
		LearningGoal:  report.LearningGoal,
		OverallScore:  report.OverallScore,
		AbilityScores: report.AbilityScores,
		Strengths:     report.Strengths,
		Weaknesses:    report.Weaknesses,
		Summary:       report.Summary,
		CreatedAt:     report.CreatedAt,
	}, nil
}

// GetLatestTrainingAttempts 从最近一次已保存评价报告读取训练事实。
// 该只读方法供成长 Dashboard 聚合使用，不改变 Tool 暴露面。
func (s *StudentGrowthDataService) GetLatestTrainingAttempts(ctx context.Context, studentID string) ([]*imodel.TrainingAttempt, error) {
	if s.mysqlStore == nil {
		return []*imodel.TrainingAttempt{}, nil
	}
	report, err := s.mysqlStore.LoadLatestEvaluationReport(ctx, studentID)
	if err != nil || report == nil {
		return []*imodel.TrainingAttempt{}, err
	}
	return append([]*imodel.TrainingAttempt(nil), report.TrainingAttempts...), nil
}

func (s *StudentGrowthDataService) SearchTrainingCases(ctx context.Context, studentID, abilityGap string, limit int) ([]TrainingCase, error) {
	if limit <= 0 || limit > 10 {
		limit = 3
	}
	seen := make(map[string]bool)
	var docs []*schema.Document
	collect := func(ownerID string) {
		if s.milvusStore != nil {
			if results, err := s.milvusStore.RetrieveByUser(ctx, ownerID, abilityGap); err == nil {
				for _, doc := range results {
					if doc != nil && !seen[doc.ID] {
						seen[doc.ID] = true
						docs = append(docs, doc)
					}
				}
			}
		}
		if s.bm25Manager != nil {
			if results, err := s.bm25Manager.Retrieve(ctx, ownerID, abilityGap); err == nil {
				for _, doc := range results {
					if doc != nil && !seen[doc.ID] {
						seen[doc.ID] = true
						docs = append(docs, doc)
					}
				}
			}
		}
	}

	collect(studentID)
	if len(docs) == 0 && studentID != "default_user" {
		collect("default_user")
	}
	if len(docs) > limit {
		docs = docs[:limit]
	}

	result := make([]TrainingCase, 0, len(docs))
	for _, doc := range docs {
		content, reference := splitTrainingCase(doc.Content)
		result = append(result, TrainingCase{
			ID:         doc.ID,
			Content:    content,
			Reference:  reference,
			Abilities:  metadataStrings(doc.MetaData, "skills"),
			Difficulty: metadataString(doc.MetaData, "difficulty"),
		})
	}
	return result, nil
}

func (s *StudentGrowthDataService) RecommendTrainingTask(ctx context.Context, studentID, learningGoal, ability, caseContent string) (*TrainingTaskRecommendation, error) {
	abilityProfile, err := s.GetAbilityProfile(ctx, studentID)
	if err != nil {
		return nil, err
	}

	selectedAbility := imodel.NormalizeAbilityDimension(ability)
	if selectedAbility == "" {
		selectedAbility = imodel.NormalizeAbilityDimension(learningGoal)
	}
	weakestScore := 0.0
	if selectedAbility == "" {
		selectedAbility, weakestScore = weakestProfileAbility(abilityProfile)
	}
	if selectedAbility == "" {
		legacyProfile, profileErr := s.GetStudentProfile(ctx, studentID)
		if profileErr != nil {
			return nil, profileErr
		}
		if len(legacyProfile.Weaknesses) > 0 {
			sort.SliceStable(legacyProfile.Weaknesses, func(i, j int) bool {
				return legacyProfile.Weaknesses[i].Score < legacyProfile.Weaknesses[j].Score
			})
			selectedAbility = imodel.NormalizeAbilityDimension(legacyProfile.Weaknesses[0].Ability)
		}
	}
	if selectedAbility == "" {
		selectedAbility = imodel.AbilityProblemSolving
	}

	skillName := imodel.AbilitySkillName(selectedAbility)
	task := strings.TrimSpace(caseContent)
	if task == "" {
		task = defaultTrainingTask(skillName)
	}
	reason := fmt.Sprintf("围绕“%s”选择 %s，并优先使用能显化学生思考过程的训练任务。", selectedAbility, skillName)
	if weakestScore > 0 {
		reason = fmt.Sprintf("长期能力画像中 %s 得分最低（%.2f），因此优先选择 %s。", selectedAbility, weakestScore, skillName)
	}
	return &TrainingTaskRecommendation{
		SkillName:    skillName,
		Ability:      selectedAbility,
		Objective:    "通过作答证据定位当前能力问题，并形成一个可执行的改进动作。",
		TrainingTask: task,
		Reason:       reason,
	}, nil
}

func (s *StudentGrowthDataService) SaveGrowthRecord(ctx context.Context, studentID string, input GrowthRecordInput) (*SavedGrowthRecord, error) {
	now := input.TrainingTime
	if now.IsZero() {
		now = time.Now()
	}
	if input.SessionID == "" {
		input.SessionID = fmt.Sprintf("growth_%d", now.UnixNano())
	}

	if s.store != nil {
		profile, err := s.store.LoadProfile(ctx, studentID)
		if err != nil {
			return nil, err
		}
		if profile == nil {
			profile = &memory.UserProfile{UserID: studentID, SkillLevel: map[string]string{}}
		}
		profile.InterviewHist = append(profile.InterviewHist, memory.InterviewRecord{
			SessionID:    input.SessionID,
			LearningGoal: input.LearningGoal,
			OverallScore: input.OverallScore,
			Date:         now,
		})
		for ability, score := range input.AbilityScores {
			profile.WeakPoints = updateAbilityWeakness(profile.WeakPoints, ability, score, now)
		}
		profile.UpdatedAt = now
		if err := s.store.SaveProfile(ctx, profile); err != nil {
			return nil, err
		}
	}

	if s.mysqlStore != nil {
		report := &imodel.EvaluationReport{
			SessionID:        input.SessionID,
			StudentID:        studentID,
			LearningGoal:     input.LearningGoal,
			OverallScore:     input.OverallScore,
			AbilityScores:    input.AbilityScores,
			Strengths:        input.Strengths,
			Weaknesses:       input.Weaknesses,
			Summary:          input.Summary,
			TrainingAttempts: input.TrainingAttempts,
			CreatedAt:        now,
		}
		reportJSON, err := json.Marshal(report)
		if err != nil {
			return nil, err
		}
		record := memory.InterviewRecord{
			SessionID: input.SessionID, LearningGoal: input.LearningGoal,
			OverallScore: input.OverallScore, Date: now,
		}
		if err := s.mysqlStore.SaveInterviewRecord(ctx, studentID, record, string(reportJSON), "{}"); err != nil {
			return nil, err
		}
	}

	return &SavedGrowthRecord{StudentID: studentID, SessionID: input.SessionID, SavedAt: now}, nil
}

func trainingAttemptIDs(attempts []*imodel.TrainingAttempt) []string {
	ids := make([]string, 0, len(attempts))
	seen := make(map[string]bool)
	for _, attempt := range attempts {
		if attempt == nil || attempt.ID == "" || attempt.EvaluationResult == nil || seen[attempt.ID] {
			continue
		}
		seen[attempt.ID] = true
		ids = append(ids, attempt.ID)
	}
	return ids
}

// RecommendStartingDifficulty 根据已记录的最低能力分选择下一轮起始难度。
func RecommendStartingDifficulty(profile *imodel.StudentAbilityProfile) imodel.DifficultyLevel {
	ability, score := weakestProfileAbility(profile)
	if ability == "" {
		return imodel.DifficultyMedium
	}
	switch {
	case score < 0.45:
		return imodel.DifficultyEasy
	case score >= 0.8:
		return imodel.DifficultyHard
	default:
		return imodel.DifficultyMedium
	}
}

func defaultTrainingTask(skillName string) string {
	switch skillName {
	case "communication-training":
		return "请用不超过 80 字向一名低年级同学解释你最近学会的一个概念，并说明你如何确认对方听懂了。"
	case "logical-thinking":
		return "请选取一个最近做出的判断，依次写出前提、推理步骤、结论和一个可能的反例。"
	case "critical-thinking":
		return "请选择一条最近看到的学习观点，分析其来源、证据、隐藏假设和结论边界。"
	case "reflection-training":
		return "请复盘一次没有达到预期的学习任务，定位过程断点，并提出一个下次可观察的改进行动。"
	default:
		return "请描述一个当前学习难题，明确目标与限制，提出两个方案，并说明如何验证哪个方案更有效。"
	}
}

func weakestProfileAbility(profile *imodel.StudentAbilityProfile) (string, float64) {
	if profile == nil || len(profile.AbilityScores) == 0 {
		return "", 0
	}
	selected := ""
	selectedScore := 0.0
	for _, ability := range imodel.CoreAbilityDimensions() {
		score, ok := profile.AbilityScores[ability]
		if !ok {
			continue
		}
		if selected == "" || score < selectedScore {
			selected = ability
			selectedScore = score
		}
	}
	return selected, selectedScore
}

func cloneScores(scores map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(scores))
	for ability, score := range scores {
		result[ability] = score
	}
	return result
}

func normalizeAbilityScore(score float64) float64 {
	if score > 1 {
		score /= 100
	}
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func roundAbilityScore(score float64) float64 {
	return math.Round(score*10000) / 10000
}

func mergeProfileStatements(existing, incoming []string, limit int) []string {
	result := make([]string, 0, len(existing)+len(incoming))
	seen := make(map[string]bool)
	for _, values := range [][]string{existing, incoming} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, value)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

func splitTrainingCase(content string) (string, string) {
	for _, marker := range []string{"\n参考答案：", "\n参考答案:"} {
		if index := strings.Index(content, marker); index >= 0 {
			return strings.TrimSpace(content[:index]), strings.TrimSpace(content[index+len(marker):])
		}
	}
	return strings.TrimSpace(content), ""
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func metadataStrings(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	switch value := metadata[key].(type) {
	case []string:
		return value
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func updateAbilityWeakness(points []memory.WeakPoint, ability string, score float64, now time.Time) []memory.WeakPoint {
	for i := range points {
		if points[i].Topic != ability {
			continue
		}
		if score >= 80 {
			return append(points[:i], points[i+1:]...)
		}
		points[i].Score = score
		points[i].HitCount++
		if score < 60 {
			points[i].WrongCount++
		}
		points[i].LastSeen = now
		return points
	}
	if score < 60 {
		points = append(points, memory.WeakPoint{
			Topic: ability, Score: score, HitCount: 1, WrongCount: 1, LastSeen: now,
		})
	}
	return points
}
