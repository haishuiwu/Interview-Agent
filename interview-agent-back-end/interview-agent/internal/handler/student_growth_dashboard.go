package handler

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleStudentGrowthDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	authenticatedStudentID, err := s.authenticateHTTPRequest(r)
	if err != nil {
		http.Error(w, "未授权：token 无效或缺失", http.StatusUnauthorized)
		return
	}
	studentID := strings.TrimSpace(r.URL.Query().Get("student_id"))
	if studentID == "" {
		http.Error(w, "student_id 不能为空", http.StatusBadRequest)
		return
	}
	if s.cfg.AuthService != nil && studentID != authenticatedStudentID {
		http.Error(w, "无权查看其他学生的成长画像", http.StatusForbidden)
		return
	}

	dashboard, err := s.cfg.GrowthDashboardService.GetDashboard(r.Context(), studentID)
	if err != nil {
		http.Error(w, "读取学生成长画像失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(dashboard); err != nil {
		return
	}
}
