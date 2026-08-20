// Package educationtool provides the unified StudentCoach education Tool Registry.
package educationtool

import (
	"context"
	"fmt"
	"sync"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
)

// Registry centrally registers StudentCoach data tools in stable order.
type Registry struct {
	mu      sync.RWMutex
	tools   []einotool.BaseTool
	byName  map[string]einotool.BaseTool
	service service.StudentGrowthService
}

// NewRegistry creates an empty Registry.
func NewRegistry(growthService service.StudentGrowthService) *Registry {
	return &Registry{byName: make(map[string]einotool.BaseTool), service: growthService}
}

// Register adds an Eino Tool and rejects duplicate names.
func (r *Registry) Register(ctx context.Context, candidate einotool.BaseTool) error {
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
	r.tools = append(r.tools, candidate)
	return nil
}

// Tools returns a stable copy of the registered Tool list.
func (r *Registry) Tools() []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]einotool.BaseTool(nil), r.tools...)
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

type runtimeContextKey struct{}

// Runtime stores the authenticated student identity and Go growth service.
type Runtime struct {
	StudentID string
	Service   service.StudentGrowthService
}

// WithRuntime injects student identity and data service into a Tool request context.
func WithRuntime(ctx context.Context, studentID string, growthService service.StudentGrowthService) context.Context {
	return context.WithValue(ctx, runtimeContextKey{}, Runtime{StudentID: studentID, Service: growthService})
}

// HasRuntime reports whether the request can safely call student data tools.
func HasRuntime(ctx context.Context) bool {
	runtime, ok := ctx.Value(runtimeContextKey{}).(Runtime)
	return ok && runtime.StudentID != "" && runtime.Service != nil
}

// NewEducationRegistry creates and registers all student growth data tools.
func NewEducationRegistry(growthService service.StudentGrowthService) (*Registry, error) {
	r := NewRegistry(growthService)
	builders := []func() (einotool.BaseTool, error){
		func() (einotool.BaseTool, error) {
			return utils.InferTool("get_student_profile", "获取当前学生的能力画像、能力等级与已知短板。需要个性化训练时应优先调用；学生身份由系统上下文提供。", r.getStudentProfile)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("get_ability_profile", "获取当前学生跨训练维护的五维能力分、优势、短板、成长历史与最近训练时间。通用训练请求应优先调用。", r.getAbilityProfile)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("get_growth_history", "按目标能力查询学生历史训练记录，用于发现连续问题和训练变化。", r.getGrowthHistory)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("get_ability_report", "获取学生最近一次能力评价结果；只有需要最近评分、强项或弱项证据时调用。", r.getAbilityReport)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("search_training_case", "根据能力短板从现有 RAG 题库检索学生训练案例。", r.searchTrainingCase)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("recommend_training_task", "让 Go 成长服务结合学生画像、学习目标、目标能力和检索案例推荐训练任务及对应 Skill。", r.recommendTrainingTask)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("update_ability_profile", "把已有真实评价结果交给 Go Service 聚合能力变化、更新长期能力画像并保存成长记录；不得传入臆测分数。", r.updateAbilityProfile)
		},
		func() (einotool.BaseTool, error) {
			return utils.InferTool("save_growth_record", "保存已经由 Go 评价流程形成的一轮训练结果。仅在已有真实评分与反馈时调用，不得自行编造结果。", r.saveGrowthRecord)
		},
	}
	for _, build := range builders {
		candidate, err := build()
		if err != nil {
			return nil, fmt.Errorf("build education tool: %w", err)
		}
		if err := r.Register(context.Background(), candidate); err != nil {
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
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.GetStudentProfile(ctx, runtime.StudentID)
}

func (r *Registry) getAbilityProfile(ctx context.Context, _ emptyRequest) (*imodel.StudentAbilityProfile, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.GetAbilityProfile(ctx, runtime.StudentID)
}

func (r *Registry) getGrowthHistory(ctx context.Context, request growthHistoryRequest) ([]service.GrowthHistoryItem, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.GetGrowthHistory(ctx, runtime.StudentID, request.Ability, request.Limit)
}

func (r *Registry) getAbilityReport(ctx context.Context, _ emptyRequest) (*service.AbilityReportSnapshot, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.GetAbilityReport(ctx, runtime.StudentID)
}

func (r *Registry) searchTrainingCase(ctx context.Context, request trainingCaseRequest) ([]service.TrainingCase, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.SearchTrainingCases(ctx, runtime.StudentID, request.AbilityGap, request.Limit)
}

func (r *Registry) recommendTrainingTask(ctx context.Context, request recommendTrainingTaskRequest) (*service.TrainingTaskRecommendation, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.RecommendTrainingTask(
		ctx,
		runtime.StudentID,
		request.LearningGoal,
		request.Ability,
		request.CaseContent,
	)
}

func (r *Registry) updateAbilityProfile(ctx context.Context, request saveGrowthRecordRequest) (*service.AbilityProfileUpdateResult, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.UpdateAbilityProfile(ctx, runtime.StudentID, growthRecordInput(request))
}

func (r *Registry) saveGrowthRecord(ctx context.Context, request saveGrowthRecordRequest) (*service.SavedGrowthRecord, error) {
	runtime, err := r.resolveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Service.SaveGrowthRecord(ctx, runtime.StudentID, growthRecordInput(request))
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
