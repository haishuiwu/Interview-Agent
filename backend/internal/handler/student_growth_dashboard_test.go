package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	growthservice "interview-agent/internal/service"
)

func TestGetStudentGrowthDashboard(t *testing.T) {
	const (
		studentID = "student-api-dashboard"
		sessionID = "session-api-dashboard"
	)
	store := memory.NewMemoryStore()
	ctx := context.Background()
	if err := store.SaveAbilityProfile(ctx, &imodel.StudentAbilityProfile{
		StudentID:     studentID,
		AbilityScores: map[string]float64{imodel.AbilityCommunication: 0.65},
		GrowthHistory: []imodel.AbilityGrowthRecord{{
			SessionID:          sessionID,
			TrainingAttemptIDs: []string{"attempt-api-dashboard"},
			LearningGoal:       "提升表达能力",
			BeforeScores:       map[string]float64{imodel.AbilityCommunication: 0.55},
			AfterScores:        map[string]float64{imodel.AbilityCommunication: 0.65},
			ScoreChanges:       map[string]float64{imodel.AbilityCommunication: 0.10},
			TrainingTime:       time.Now(),
		}},
	}); err != nil {
		t.Fatalf("SaveAbilityProfile: %v", err)
	}
	traceService := growthservice.NewAgentTraceService(store)
	if _, err := traceService.Create(ctx, sessionID, studentID, "improve communication"); err != nil {
		t.Fatalf("Create trace: %v", err)
	}
	if err := traceService.UpdateSkill(ctx, sessionID, "communication-training", "communication is the current focus"); err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	growthDataService := growthservice.NewStudentGrowthDataService(store, nil, nil, nil)
	dashboardService := growthservice.NewStudentGrowthDashboardService(growthDataService, traceService)
	server := NewServer(&ServerConfig{
		CombinedStore:          store,
		AgentTraceService:      traceService,
		GrowthDashboardService: dashboardService,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/student/growth/dashboard?student_id="+studentID, nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	dashboard := &imodel.StudentGrowthDashboard{}
	if err := json.NewDecoder(recorder.Body).Decode(dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if dashboard.StudentID != studentID || dashboard.Abilities[imodel.AbilityCommunication].Score != 0.65 {
		t.Fatalf("dashboard = %+v", dashboard)
	}
	if len(dashboard.RecentTrainings) != 1 || dashboard.RecentTrainings[0].Skill != "communication-training" {
		t.Fatalf("recent trainings = %#v", dashboard.RecentTrainings)
	}
}
