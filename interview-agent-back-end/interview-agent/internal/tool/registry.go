// Package educationtool provides the unified StudentCoach education Tool Registry.
package educationtool

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
)

// ToolType 表示 Tool 对学生长期数据的访问权限。
type ToolType string

const (
	ReadTool  ToolType = "read"
	WriteTool ToolType = "write"
)

// Registry centrally registers StudentCoach data tools in stable order.
type Registry struct {
	mu        sync.RWMutex
	tools     []einotool.BaseTool
	byName    map[string]einotool.BaseTool
	toolTypes map[string]ToolType
	service   service.StudentGrowthService
}

// NewRegistry creates an empty Registry.
func NewRegistry(growthService service.StudentGrowthService) *Registry {
	return &Registry{
		byName:    make(map[string]einotool.BaseTool),
		toolTypes: make(map[string]ToolType),
		service:   growthService,
	}
}

// Register 注册只读 Tool。需要注册写 Tool 时必须显式调用 RegisterAs。
func (r *Registry) Register(ctx context.Context, candidate einotool.BaseTool) error {
	return r.RegisterAs(ctx, candidate, ReadTool)
}

// RegisterAs 按权限类型注册 Eino Tool，并拒绝重复名称和未知权限。
func (r *Registry) RegisterAs(ctx context.Context, candidate einotool.BaseTool, toolType ToolType) error {
	if toolType != ReadTool && toolType != WriteTool {
		return fmt.Errorf("education tool registry: unsupported tool type %q", toolType)
	}
	info, err := candidate.Info(ctx)
	if err != nil {
		return fmt.Errorf("education tool registry: read info: %w", err)
	}
	if info == nil || info.Name == "" {
		return fmt.Errorf("education tool registry: tool name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byName[info.Name]; exists {
		return fmt.Errorf("education tool registry: duplicate tool %q", info.Name)
	}
	r.byName[info.Name] = candidate
	r.toolTypes[info.Name] = toolType
	r.tools = append(r.tools, candidate)
	return nil
}

// Tools returns a stable copy of the registered Tool list.
func (r *Registry) Tools() []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]einotool.BaseTool(nil), r.tools...)
}

// ToolsByType 返回指定权限类型的稳定 Tool 列表。
func (r *Registry) ToolsByType(toolType ToolType) []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]einotool.BaseTool, 0, len(r.tools))
	for _, candidate := range r.tools {
		info, err := candidate.Info(context.Background())
		if err != nil || info == nil || r.toolTypes[info.Name] != toolType {
			continue
		}
		tools = append(tools, candidate)
	}
	return tools
}

// ReadTools 返回允许 LLM Agent 调用的只读 Tool。
func (r *Registry) ReadTools() []einotool.BaseTool {
	return r.ToolsByType(ReadTool)
}

// WriteTools 返回仅供受信任 Go 流程识别的写 Tool；不得绑定给 LLM Agent。
func (r *Registry) WriteTools() []einotool.BaseTool {
	return r.ToolsByType(WriteTool)
}

// Names returns all registered Tool names.
func (r *Registry) Names(ctx context.Context) []string {
	tools := r.Tools()
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		if info, err := candidate.Info(ctx); err == nil && info != nil {
			names = append(names, info.Name)
		}
	}
	return names
}

// NamesByType 返回指定权限类型的 Tool 名称。
func (r *Registry) NamesByType(ctx context.Context, toolType ToolType) []string {
	tools := r.ToolsByType(toolType)
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		if info, err := candidate.Info(ctx); err == nil && info != nil {
			names = append(names, info.Name)
		}
	}
	return names
}

type runtimeContextKey struct{}

// Runtime stores the authenticated student identity and Go growth service.
type Runtime struct {
	StudentID    string
	Service      service.StudentGrowthService
	SessionID    string
	TraceService *service.AgentTraceService
}

// WithRuntime injects student identity and data service into a Tool request context.
func WithRuntime(ctx context.Context, studentID string, growthService service.StudentGrowthService) context.Context {
	runtime, _ := ctx.Value(runtimeContextKey{}).(Runtime)
	runtime.StudentID = studentID
	runtime.Service = growthService
	return context.WithValue(ctx, runtimeContextKey{}, runtime)
}

// WithTrace 为只读 Tool 调用注入当前业务 Trace；它不改变 Tool 权限或注册列表。
func WithTrace(ctx context.Context, sessionID string, traceService *service.AgentTraceService) context.Context {
	runtime, _ := ctx.Value(runtimeContextKey{}).(Runtime)
	runtime.SessionID = sessionID
	runtime.TraceService = traceService
	return context.WithValue(ctx, runtimeContextKey{}, runtime)
}

// HasRuntime reports whether the request can safely call student data tools.
func HasRuntime(ctx context.Context) bool {
	runtime, ok := ctx.Value(runtimeContextKey{}).(Runtime)
	return ok && runtime.StudentID != "" && runtime.Service != nil
}

// NewEducationRegistry creates and registers all student growth data tools.
func NewEducationRegistry(growthService service.StudentGrowthService) (*Registry, error) {
	r := NewRegistry(growthService)
	builders := []struct {
		toolType ToolType
		build    func() (einotool.BaseTool, error)
	}{
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("get_student_profile", "获取当前学生的能力画像、能力等级与已知短板。需要个性化训练时应优先调用；学生身份由系统上下文提供。", r.getStudentProfile)
		}},
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("get_ability_profile", "获取当前学生跨训练维护的五维能力分、优势、短板、成长历史与最近训练时间。通用训练请求应优先调用。", r.getAbilityProfile)
		}},
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("get_growth_history", "按目标能力查询学生历史训练记录，用于发现连续问题和训练变化。", r.getGrowthHistory)
		}},
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("get_ability_report", "获取学生最近一次能力评价结果；只有需要最近评分、强项或弱项证据时调用。", r.getAbilityReport)
		}},
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("search_training_case", "根据能力短板从现有 RAG 题库检索学生训练案例。", r.searchTrainingCase)
		}},
		{ReadTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("recommend_training_task", "让 Go 成长服务结合学生画像、学习目标、目标能力和检索案例推荐训练任务及对应 Skill。", r.recommendTrainingTask)
		}},
		{WriteTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("update_ability_profile", "把已有真实评价结果交给 Go Service 聚合能力变化、更新长期能力画像并保存成长记录；不得传入臆测分数。", r.updateAbilityProfile)
		}},
		{WriteTool, func() (einotool.BaseTool, error) {
			return utils.InferTool("save_growth_record", "保存已经由 Go 评价流程形成的一轮训练结果。仅在已有真实评分与反馈时调用，不得自行编造结果。", r.saveGrowthRecord)
		}},
	}
	for _, builder := range builders {
		candidate, err := builder.build()
		if err != nil {
			return nil, fmt.Errorf("build education tool: %w", err)
		}
		if err := r.RegisterAs(context.Background(), candidate, builder.toolType); err != nil {
			return nil, err
		}
	}
	return r, nil
}

type emptyRequest struct{}

type growthHistoryRequest struct {
	Ability string `json:"ability" jsonschema:"description=要查询的学生能力，例如表达能力"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=最多返回的历史记录数，建议 3 到 5"`
}

type trainingCaseRequest struct {
	AbilityGap string `json:"ability_gap" jsonschema:"description=需要训练的能力短板，例如表达能力"`
	Limit      int    `json:"limit,omitempty" jsonschema:"description=最多返回的训练案例数，建议 1 到 3"`
}

type recommendTrainingTaskRequest struct {
	LearningGoal string `json:"learning_goal" jsonschema:"description=学生本轮学习目标"`
	Ability      string `json:"ability" jsonschema:"description=本轮重点训练的能力"`
	CaseContent  string `json:"case_content,omitempty" jsonschema:"description=检索到并选中的训练案例内容"`
}

type saveGrowthRecordRequest struct {
	SessionID     string             `json:"session_id,omitempty"`
	LearningGoal  string             `json:"learning_goal"`
	OverallScore  float64            `json:"overall_score"`
	AbilityScores map[string]float64 `json:"ability_scores,omitempty"`
	Strengths     []string           `json:"strengths,omitempty"`
	Weaknesses    []string           `json:"weaknesses,omitempty"`
	Summary       string             `json:"summary,omitempty"`
}

func (r *Registry) resolveRuntime(ctx context.Context) (Runtime, error) {
	runtime, _ := ctx.Value(runtimeContextKey{}).(Runtime)
	if runtime.Service == nil {
		runtime.Service = r.service
	}
	if runtime.StudentID == "" {
		return Runtime{}, fmt.Errorf("education tool: student identity is missing")
	}
	if runtime.Service == nil {
		return Runtime{}, fmt.Errorf("education tool: growth service is unavailable")
	}
	return runtime, nil
}

func (r *Registry) getStudentProfile(ctx context.Context, _ emptyRequest) (*service.StudentProfileSnapshot, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "get_student_profile", startedAt, err, "student profile was not loaded")
		return nil, err
	}
	result, err := runtime.Service.GetStudentProfile(ctx, runtime.StudentID)
	r.recordToolTrace(ctx, "get_student_profile", startedAt, err, "loaded student profile summary")
	if err == nil && result != nil {
		r.recordMemorySummary(ctx,
			"student_profile=loaded",
			fmt.Sprintf("student_profile_abilities=%d", len(result.AbilityLevels)),
			fmt.Sprintf("student_profile_weaknesses=%d", len(result.Weaknesses)),
		)
	}
	return result, err
}

func (r *Registry) getAbilityProfile(ctx context.Context, _ emptyRequest) (*imodel.StudentAbilityProfile, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "get_ability_profile", startedAt, err, "ability profile was not loaded")
		return nil, err
	}
	result, err := runtime.Service.GetAbilityProfile(ctx, runtime.StudentID)
	r.recordToolTrace(ctx, "get_ability_profile", startedAt, err, "loaded student ability profile")
	if err == nil && result != nil {
		r.recordMemorySummary(ctx, abilityScoreSummaries(result.AbilityScores)...)
	}
	return result, err
}

func (r *Registry) getGrowthHistory(ctx context.Context, request growthHistoryRequest) ([]service.GrowthHistoryItem, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "get_growth_history", startedAt, err, "growth history was not loaded")
		return nil, err
	}
	result, err := runtime.Service.GetGrowthHistory(ctx, runtime.StudentID, request.Ability, request.Limit)
	r.recordToolTrace(ctx, "get_growth_history", startedAt, err, fmt.Sprintf("loaded %d growth history entries", len(result)))
	if err == nil {
		r.recordMemorySummary(ctx, fmt.Sprintf("growth_history_entries=%d", len(result)))
	}
	return result, err
}

func (r *Registry) getAbilityReport(ctx context.Context, _ emptyRequest) (*service.AbilityReportSnapshot, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "get_ability_report", startedAt, err, "ability report was not loaded")
		return nil, err
	}
	result, err := runtime.Service.GetAbilityReport(ctx, runtime.StudentID)
	r.recordToolTrace(ctx, "get_ability_report", startedAt, err, "loaded latest ability report summary")
	if err == nil && result != nil {
		r.recordMemorySummary(ctx, abilityScoreSummaries(result.AbilityScores)...)
	}
	return result, err
}

func (r *Registry) searchTrainingCase(ctx context.Context, request trainingCaseRequest) ([]service.TrainingCase, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "search_training_case", startedAt, err, "training cases were not retrieved")
		return nil, err
	}
	result, err := runtime.Service.SearchTrainingCases(ctx, runtime.StudentID, request.AbilityGap, request.Limit)
	r.recordToolTrace(ctx, "search_training_case", startedAt, err, fmt.Sprintf("retrieved %d training cases", len(result)))
	return result, err
}

func (r *Registry) recommendTrainingTask(ctx context.Context, request recommendTrainingTaskRequest) (*service.TrainingTaskRecommendation, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "recommend_training_task", startedAt, err, "training task was not recommended")
		return nil, err
	}
	result, err := runtime.Service.RecommendTrainingTask(
		ctx,
		runtime.StudentID,
		request.LearningGoal,
		request.Ability,
		request.CaseContent,
	)
	summary := "recommended training task"
	if result != nil && result.SkillName != "" {
		summary = "recommended " + result.SkillName
	}
	r.recordToolTrace(ctx, "recommend_training_task", startedAt, err, summary)
	if err == nil && result != nil && runtime.TraceService != nil && runtime.SessionID != "" {
		_ = runtime.TraceService.UpdateSkill(ctx, runtime.SessionID, result.SkillName, result.Reason)
	}
	return result, err
}

func (r *Registry) updateAbilityProfile(ctx context.Context, request saveGrowthRecordRequest) (*service.AbilityProfileUpdateResult, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "update_ability_profile", startedAt, err, "ability profile was not updated")
		return nil, err
	}
	result, err := runtime.Service.UpdateAbilityProfile(ctx, runtime.StudentID, growthRecordInput(request))
	r.recordToolTrace(ctx, "update_ability_profile", startedAt, err, "updated ability profile through trusted runtime")
	return result, err
}

func (r *Registry) saveGrowthRecord(ctx context.Context, request saveGrowthRecordRequest) (*service.SavedGrowthRecord, error) {
	startedAt := time.Now()
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		r.recordToolTrace(ctx, "save_growth_record", startedAt, err, "growth record was not saved")
		return nil, err
	}
	result, err := runtime.Service.SaveGrowthRecord(ctx, runtime.StudentID, growthRecordInput(request))
	r.recordToolTrace(ctx, "save_growth_record", startedAt, err, "saved growth record through trusted runtime")
	return result, err
}

func (r *Registry) recordToolTrace(ctx context.Context, name string, startedAt time.Time, callErr error, summary string) {
	runtime, _ := ctx.Value(runtimeContextKey{}).(Runtime)
	if runtime.TraceService == nil || runtime.SessionID == "" {
		return
	}
	durationMs := time.Since(startedAt).Milliseconds()
	_ = runtime.TraceService.RecordToolCall(ctx, runtime.SessionID, imodel.ToolTrace{
		Name: name, Success: callErr == nil, DurationMs: durationMs, Summary: summary,
	})
}

func (r *Registry) recordMemorySummary(ctx context.Context, summaries ...string) {
	runtime, _ := ctx.Value(runtimeContextKey{}).(Runtime)
	if runtime.TraceService == nil || runtime.SessionID == "" || len(summaries) == 0 {
		return
	}
	_ = runtime.TraceService.RecordMemorySummary(ctx, runtime.SessionID, summaries...)
}

func abilityScoreSummaries(scores map[string]float64) []string {
	abilities := make([]string, 0, len(scores))
	values := make(map[string]float64, len(scores))
	for rawAbility, score := range scores {
		if ability := imodel.NormalizeAbilityDimension(rawAbility); ability != "" {
			abilities = append(abilities, ability)
			values[ability] = score
		}
	}
	sort.Strings(abilities)
	summaries := make([]string, 0, len(abilities))
	for _, ability := range abilities {
		summaries = append(summaries, fmt.Sprintf("%s=%.2f", ability, values[ability]))
	}
	return summaries
}

func growthRecordInput(request saveGrowthRecordRequest) service.GrowthRecordInput {
	return service.GrowthRecordInput{
		SessionID:     request.SessionID,
		LearningGoal:  request.LearningGoal,
		OverallScore:  request.OverallScore,
		AbilityScores: request.AbilityScores,
		Strengths:     request.Strengths,
		Weaknesses:    request.Weaknesses,
		Summary:       request.Summary,
	}
}
