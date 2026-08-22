package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
	"interview-agent/internal/service"
)

func loadFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var fixture T
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return fixture
}

func reportMetric(t *testing.T, metric string, passed, total int) float64 {
	t.Helper()
	accuracy := 0.0
	if total > 0 {
		accuracy = float64(passed) / float64(total)
	}
	t.Logf("\n| Metric | Passed | Samples | Result |\n|---|---:|---:|---:|\n| %s | %d | %d | %.2f%% |", metric, passed, total, accuracy*100)
	return accuracy
}

type mockToolCall struct {
	name      string
	arguments string
}

type scriptedToolModel struct {
	calls             []mockToolCall
	step              int
	requireToolResult string
	finalContent      string
}

func (m *scriptedToolModel) BindTools([]*schema.ToolInfo) error { return nil }

func (m *scriptedToolModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.step >= len(m.calls) {
		if m.requireToolResult != "" {
			var results strings.Builder
			for _, message := range input {
				if message.Role == schema.Tool {
					results.WriteString(message.Content)
				}
			}
			if !strings.Contains(results.String(), m.requireToolResult) {
				return nil, fmt.Errorf("required tool result %q not found in %s", m.requireToolResult, results.String())
			}
		}
		content := m.finalContent
		if content == "" {
			content = "已根据学生成长数据生成针对性训练建议。"
		}
		return schema.AssistantMessage(content, nil), nil
	}
	call := m.calls[m.step]
	m.step++
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   fmt.Sprintf("benchmark-call-%d", m.step),
		Type: "function",
		Function: schema.FunctionCall{
			Name:      call.name,
			Arguments: call.arguments,
		},
	}}), nil
}

func (m *scriptedToolModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

type recordingGrowthService struct {
	delegate service.StudentGrowthService
	calls    []string
}

func (s *recordingGrowthService) GetStudentProfile(ctx context.Context, studentID string) (*service.StudentProfileSnapshot, error) {
	s.calls = append(s.calls, "get_student_profile")
	return s.delegate.GetStudentProfile(ctx, studentID)
}

func (s *recordingGrowthService) GetAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error) {
	s.calls = append(s.calls, "get_ability_profile")
	return s.delegate.GetAbilityProfile(ctx, studentID)
}

func (s *recordingGrowthService) UpdateAbilityProfile(ctx context.Context, studentID string, input service.GrowthRecordInput) (*service.AbilityProfileUpdateResult, error) {
	s.calls = append(s.calls, "update_ability_profile")
	return s.delegate.UpdateAbilityProfile(ctx, studentID, input)
}

func (s *recordingGrowthService) GetGrowthHistory(ctx context.Context, studentID, ability string, limit int) ([]service.GrowthHistoryItem, error) {
	s.calls = append(s.calls, "get_growth_history")
	return s.delegate.GetGrowthHistory(ctx, studentID, ability, limit)
}

func (s *recordingGrowthService) GetAbilityReport(ctx context.Context, studentID string) (*service.AbilityReportSnapshot, error) {
	s.calls = append(s.calls, "get_ability_report")
	return s.delegate.GetAbilityReport(ctx, studentID)
}

func (s *recordingGrowthService) SearchTrainingCases(ctx context.Context, studentID, abilityGap string, limit int) ([]service.TrainingCase, error) {
	s.calls = append(s.calls, "search_training_case")
	return s.delegate.SearchTrainingCases(ctx, studentID, abilityGap, limit)
}

func (s *recordingGrowthService) RecommendTrainingTask(ctx context.Context, studentID, learningGoal, ability, caseContent string) (*service.TrainingTaskRecommendation, error) {
	s.calls = append(s.calls, "recommend_training_task")
	return s.delegate.RecommendTrainingTask(ctx, studentID, learningGoal, ability, caseContent)
}

func (s *recordingGrowthService) SaveGrowthRecord(ctx context.Context, studentID string, input service.GrowthRecordInput) (*service.SavedGrowthRecord, error) {
	s.calls = append(s.calls, "save_growth_record")
	return s.delegate.SaveGrowthRecord(ctx, studentID, input)
}

func exactChain(actual, expected []string) bool {
	return reflect.DeepEqual(actual, expected)
}
