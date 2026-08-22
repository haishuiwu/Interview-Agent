package service

import (
	"context"
	"testing"
	"time"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
)

type dashboardGrowthStub struct {
	profile  *imodel.StudentAbilityProfile
	attempts []*imodel.TrainingAttempt
}

func (s *dashboardGrowthStub) GetAbilityProfile(context.Context, string) (*imodel.StudentAbilityProfile, error) {
	return s.profile, nil
}

func (s *dashboardGrowthStub) GetLatestTrainingAttempts(context.Context, string) ([]*imodel.TrainingAttempt, error) {
	return s.attempts, nil
}

func dashboardFixture(t *testing.T) (*StudentGrowthDashboardService, string) {
	t.Helper()
	const (
		studentID = "student-dashboard-001"
		sessionID = "session-dashboard-001"
		attemptID = "attempt-dashboard-001"
	)
	trainingTime := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	growth := &dashboardGrowthStub{
		profile: &imodel.StudentAbilityProfile{
			StudentID: studentID,
			Summary:   "表达结构比之前完整",
			AbilityScores: map[string]float64{
				imodel.AbilityCommunication: 0.65,
			},
			Strengths:  []string{"能够主动澄清问题"},
			Weaknesses: []string{"表达结构"},
			GrowthHistory: []imodel.AbilityGrowthRecord{{
				SessionID:          sessionID,
				TrainingAttemptIDs: []string{attemptID},
				LearningGoal:       "提升表达能力",
				BeforeScores:       map[string]float64{imodel.AbilityCommunication: 0.55},
				AfterScores:        map[string]float64{imodel.AbilityCommunication: 0.65},
				ScoreChanges:       map[string]float64{imodel.AbilityCommunication: 0.10},
				OverallScore:       0.70,
				TrainingTime:       trainingTime,
			}},
		},
		attempts: []*imodel.TrainingAttempt{{
			ID:        attemptID,
			StudentID: studentID,
			SkillName: "communication-training",
			Rubric: []imodel.EvaluationCriterion{{
				Ability: imodel.AbilityCommunication,
			}},
			EvaluationResult: &imodel.EvaluationResult{
				Feedback: "回答结构比之前完整",
			},
			Status: imodel.TrainingAttemptStatusEvaluated,
		}},
	}
	traceService := NewAgentTraceService(memory.NewMemoryStore())
	ctx := context.Background()
	if _, err := traceService.Create(ctx, sessionID, studentID, "improve communication"); err != nil {
		t.Fatalf("Create trace: %v", err)
	}
	if err := traceService.UpdateSkill(ctx, sessionID, "communication-training", "communication ability needs focused practice"); err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	if err := traceService.BindTrainingAttempt(ctx, sessionID, attemptID); err != nil {
		t.Fatalf("BindTrainingAttempt: %v", err)
	}
	return NewStudentGrowthDashboardService(growth, traceService), studentID
}

func TestStudentGrowthDashboardReturnsCurrentAbilityScore(t *testing.T) {
	service, studentID := dashboardFixture(t)
	dashboard, err := service.GetDashboard(context.Background(), studentID)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	communication := dashboard.Abilities[imodel.AbilityCommunication]
	if communication.Score != 0.65 || communication.Trend != imodel.AbilityTrendUp || communication.RecentChange != 0.10 {
		t.Fatalf("communication snapshot = %+v", communication)
	}
	if len(communication.Evidence) == 0 || communication.Evidence[0] != "回答结构比之前完整" {
		t.Fatalf("communication evidence = %#v", communication.Evidence)
	}
}

func TestStudentGrowthDashboardIncludesRecentTrainingAttempt(t *testing.T) {
	service, studentID := dashboardFixture(t)
	dashboard, err := service.GetDashboard(context.Background(), studentID)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if len(dashboard.RecentTrainings) != 1 {
		t.Fatalf("recent trainings = %#v", dashboard.RecentTrainings)
	}
	training := dashboard.RecentTrainings[0]
	if training.Skill != "communication-training" || training.TrainingAttemptID != "attempt-dashboard-001" || training.Result != "completed" {
		t.Fatalf("training summary = %+v", training)
	}
}

func TestStudentGrowthDashboardUsesAgentTraceForRecommendation(t *testing.T) {
	service, studentID := dashboardFixture(t)
	dashboard, err := service.GetDashboard(context.Background(), studentID)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if len(dashboard.NextRecommendations) == 0 || dashboard.NextRecommendations[0] != "继续进行表达训练" {
		t.Fatalf("recommendations = %#v", dashboard.NextRecommendations)
	}
	if dashboard.RecentTrainings[0].DecisionReason == "" {
		t.Fatal("training decision reason was not aggregated from AgentTrace")
	}
}
