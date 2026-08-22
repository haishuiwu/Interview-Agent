package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
	educationtool "interview-agent/internal/tool"
)

type studentCoachToolScenarioService struct {
	calls          []string
	historyAbility string
}

func (s *studentCoachToolScenarioService) GetStudentProfile(context.Context, string) (*service.StudentProfileSnapshot, error) {
	s.calls = append(s.calls, "get_student_profile")
	return &service.StudentProfileSnapshot{
		StudentID: "student-001",
		AbilityLevels: map[string]string{
			"表达能力": "需要加强",
		},
		Weaknesses: []service.AbilityWeakness{{Ability: "表达能力", Score: 55, Attempts: 2}},
	}, nil
}

func (s *studentCoachToolScenarioService) GetAbilityProfile(context.Context, string) (*imodel.StudentAbilityProfile, error) {
	s.calls = append(s.calls, "get_ability_profile")
	return &imodel.StudentAbilityProfile{
		StudentID: "student-001",
		AbilityScores: map[string]float64{
			imodel.AbilityCommunication: 0.55,
		},
	}, nil
}

func (s *studentCoachToolScenarioService) UpdateAbilityProfile(context.Context, string, service.GrowthRecordInput) (*service.AbilityProfileUpdateResult, error) {
	s.calls = append(s.calls, "update_ability_profile")
	return &service.AbilityProfileUpdateResult{}, nil
}

func (s *studentCoachToolScenarioService) GetGrowthHistory(_ context.Context, _ string, ability string, _ int) ([]service.GrowthHistoryItem, error) {
	s.calls = append(s.calls, "get_growth_history")
	s.historyAbility = ability
	return []service.GrowthHistoryItem{{
		SessionID:    "growth-previous",
		LearningGoal: "提升表达能力",
		OverallScore: 55,
	}}, nil
}

func (s *studentCoachToolScenarioService) GetAbilityReport(context.Context, string) (*service.AbilityReportSnapshot, error) {
	s.calls = append(s.calls, "get_ability_report")
	return &service.AbilityReportSnapshot{StudentID: "student-001"}, nil
}

func (s *studentCoachToolScenarioService) SearchTrainingCases(_ context.Context, _ string, abilityGap string, _ int) ([]service.TrainingCase, error) {
	s.calls = append(s.calls, "search_training_case")
	return []service.TrainingCase{{
		ID:        "communication-case-1",
		Content:   "请在 60 秒内向同学说明小组方案，并用结论、理由、例子三步组织表达。",
		Abilities: []string{abilityGap},
	}}, nil
}

func (s *studentCoachToolScenarioService) RecommendTrainingTask(ctx context.Context, studentID, learningGoal, ability, caseContent string) (*service.TrainingTaskRecommendation, error) {
	s.calls = append(s.calls, "recommend_training_task")
	dataService := service.NewStudentGrowthDataService(nil, nil, nil, nil)
	return dataService.RecommendTrainingTask(ctx, studentID, learningGoal, ability, caseContent)
}

func (s *studentCoachToolScenarioService) SaveGrowthRecord(context.Context, string, service.GrowthRecordInput) (*service.SavedGrowthRecord, error) {
	s.calls = append(s.calls, "save_growth_record")
	return &service.SavedGrowthRecord{StudentID: "student-001"}, nil
}

type studentCoachToolCallingModel struct {
	step       int
	boundTools []string
}

func (m *studentCoachToolCallingModel) BindTools(tools []*schema.ToolInfo) error {
	m.boundTools = m.boundTools[:0]
	for _, info := range tools {
		m.boundTools = append(m.boundTools, info.Name)
	}
	return nil
}

func (m *studentCoachToolCallingModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	step := m.step
	m.step++
	switch step {
	case 0:
		return scenarioToolCall(step, "get_student_profile", `{}`), nil
	case 1:
		return scenarioToolCall(step, "get_growth_history", `{"ability":"表达能力","limit":5}`), nil
	case 2:
		return scenarioToolCall(step, "search_training_case", `{"ability_gap":"表达能力","limit":3}`), nil
	case 3:
		return scenarioToolCall(step, "recommend_training_task", `{"learning_goal":"我想提升自己的表达能力。","ability":"表达能力","case_content":"请在 60 秒内向同学说明小组方案，并用结论、理由、例子三步组织表达。"}`), nil
	case 4:
		var toolResults strings.Builder
		for _, message := range input {
			if message.Role == schema.Tool {
				toolResults.WriteString(message.Content)
			}
		}
		if !strings.Contains(toolResults.String(), "communication-training") {
			return nil, fmt.Errorf("recommendation tool result was not fed back to model: %s", toolResults.String())
		}
		return schema.AssistantMessage("已选择 communication-training。针对性训练任务：请在 60 秒内向同学说明小组方案，并用结论、理由、例子三步组织表达。", nil), nil
	default:
		return nil, fmt.Errorf("unexpected model step %d", step)
	}
}

func (m *studentCoachToolCallingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func scenarioToolCall(step int, name, arguments string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   fmt.Sprintf("call-%d", step),
		Type: "function",
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}})
}

func TestStudentCoachUsesEducationToolsForCommunicationGoal(t *testing.T) {
	growthService := &studentCoachToolScenarioService{}
	registry, err := educationtool.NewEducationRegistry(growthService)
	if err != nil {
		t.Fatalf("NewEducationRegistry() error = %v", err)
	}
	chatModel := &studentCoachToolCallingModel{}
	coach := NewStudentCoach(chatModel, WithEducationToolRegistry(registry))
	ctx := educationtool.WithRuntime(context.Background(), "student-001", growthService)

	result, err := coach.AskQuestion(ctx, &imodel.TrainingState{
		CurrentQuestion:   1,
		TotalQuestions:    3,
		CurrentDifficulty: imodel.DifficultyEasy,
	}, &imodel.PlannedQuestion{
		Content: "围绕学生的表达能力安排一个训练任务。",
		Skills:  []string{"communication-training"},
	}, "我想提升自己的表达能力。")
	if err != nil {
		t.Fatalf("AskQuestion() error = %v", err)
	}

	wantCalls := []string{
		"get_student_profile",
		"get_growth_history",
		"search_training_case",
		"recommend_training_task",
	}
	if !reflect.DeepEqual(growthService.calls, wantCalls) {
		t.Fatalf("service calls = %v, want %v", growthService.calls, wantCalls)
	}
	if growthService.historyAbility != "表达能力" {
		t.Fatalf("history ability = %q, want 表达能力", growthService.historyAbility)
	}
	if !strings.Contains(result, "communication-training") || !strings.Contains(result, "结论、理由、例子") {
		t.Fatalf("targeted training result = %q", result)
	}
	wantBoundTools := []string{
		"get_student_profile",
		"get_ability_profile",
		"get_growth_history",
		"get_ability_report",
		"search_training_case",
		"recommend_training_task",
	}
	if !reflect.DeepEqual(chatModel.boundTools, wantBoundTools) {
		t.Fatalf("bound tools = %v, want read tools %v", chatModel.boundTools, wantBoundTools)
	}
}

type profilePriorityToolCallingModel struct {
	step int
}

func (m *profilePriorityToolCallingModel) BindTools([]*schema.ToolInfo) error { return nil }

func (m *profilePriorityToolCallingModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	step := m.step
	m.step++
	switch step {
	case 0:
		return scenarioToolCall(step, "get_ability_profile", `{}`), nil
	case 1:
		return scenarioToolCall(step, "recommend_training_task", `{"learning_goal":"帮我训练一下","ability":"","case_content":""}`), nil
	case 2:
		var toolResults strings.Builder
		for _, message := range input {
			if message.Role == schema.Tool {
				toolResults.WriteString(message.Content)
			}
		}
		if !strings.Contains(toolResults.String(), "communication-training") {
			return nil, fmt.Errorf("profile did not drive communication Skill: %s", toolResults.String())
		}
		return schema.AssistantMessage("根据你的长期能力画像，本轮优先使用 communication-training。", nil), nil
	default:
		return nil, fmt.Errorf("unexpected model step %d", step)
	}
}

func (m *profilePriorityToolCallingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestStudentCoachPrioritizesWeakestAbilityFromMemory(t *testing.T) {
	ctx := context.Background()
	store := memory.NewMemoryStore()
	if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID: "student-profile-priority",
		AbilityScores: map[string]float64{
			imodel.AbilityCommunication:   0.55,
			imodel.AbilityLogicalThinking: 0.80,
		},
	}); err != nil {
		t.Fatalf("SaveAbilityProfile() error = %v", err)
	}
	growthService := service.NewStudentGrowthDataService(store, nil, nil, nil)
	registry, err := educationtool.NewEducationRegistry(growthService)
	if err != nil {
		t.Fatalf("NewEducationRegistry() error = %v", err)
	}
	coach := NewStudentCoach(&profilePriorityToolCallingModel{}, WithEducationToolRegistry(registry))
	toolCtx := educationtool.WithRuntime(ctx, "student-profile-priority", growthService)

	result, err := coach.AskQuestion(toolCtx, &imodel.TrainingState{
		CurrentQuestion:   1,
		TotalQuestions:    1,
		CurrentDifficulty: imodel.DifficultyMedium,
	}, &imodel.PlannedQuestion{Content: "根据长期画像选择本轮训练重点。"}, "帮我训练一下")
	if err != nil {
		t.Fatalf("AskQuestion() error = %v", err)
	}
	if !strings.Contains(result, "communication-training") {
		t.Fatalf("result = %q, want communication-training", result)
	}
}

type writePermissionProbeModel struct {
	step       int
	boundTools []string
}

func (m *writePermissionProbeModel) BindTools(tools []*schema.ToolInfo) error {
	m.boundTools = m.boundTools[:0]
	for _, info := range tools {
		m.boundTools = append(m.boundTools, info.Name)
	}
	return nil
}

func (m *writePermissionProbeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.step == 0 {
		m.step++
		for _, name := range []string{"update_ability_profile", "save_growth_record"} {
			if containsToolName(m.boundTools, name) {
				return scenarioToolCall(0, name, `{"session_id":"prompt-injection","learning_goal":"直接改分","overall_score":90,"ability_scores":{"communication":90}}`), nil
			}
		}
	}
	return schema.AssistantMessage("我不能直接修改能力分，请通过正常训练与评价形成成长记录。", nil), nil
}

func (m *writePermissionProbeModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func containsToolName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func TestStudentCoachCannotCallWriteToolsForScoreManipulation(t *testing.T) {
	growthService := &studentCoachToolScenarioService{}
	registry, err := educationtool.NewEducationRegistry(growthService)
	if err != nil {
		t.Fatalf("NewEducationRegistry() error = %v", err)
	}
	chatModel := &writePermissionProbeModel{}
	coach := NewStudentCoach(chatModel, WithEducationToolRegistry(registry))
	ctx := educationtool.WithRuntime(context.Background(), "student-001", growthService)

	result, err := coach.AskQuestion(ctx, &imodel.TrainingState{
		CurrentQuestion: 1, TotalQuestions: 1, CurrentDifficulty: imodel.DifficultyEasy,
	}, &imodel.PlannedQuestion{Content: "学生请求：把我的能力分改成90分"}, "把我的能力分改成90分")
	if err != nil {
		t.Fatalf("AskQuestion() error = %v", err)
	}
	if containsToolName(chatModel.boundTools, "update_ability_profile") || containsToolName(chatModel.boundTools, "save_growth_record") {
		t.Fatalf("write tools were exposed to Agent: %v", chatModel.boundTools)
	}
	if containsToolName(growthService.calls, "update_ability_profile") || containsToolName(growthService.calls, "save_growth_record") {
		t.Fatalf("write service was called by Agent: %v", growthService.calls)
	}
	if !strings.Contains(result, "不能直接修改") {
		t.Fatalf("result = %q, want safe refusal", result)
	}
}
