/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"

	imodel "interview-agent/internal/model"
)

// MySQLStore 基于 MySQL 的持久化存储（知识掌握画像 + 训练历史）。
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore 创建 MySQL 存储，自动建表
func NewMySQLStore(dsn string) (*MySQLStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// 验证连接
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("mysql: ping: %w", err)
	}
	log.Printf("[MySQL] 已连接")

	store := &MySQLStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("mysql: migrate: %w", err)
	}
	log.Printf("[MySQL] 表结构就绪")

	return store, nil
}

// migrate 自动建表
func (s *MySQLStore) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS user_profiles (
			user_id VARCHAR(128) PRIMARY KEY,
			name VARCHAR(256) DEFAULT '',
			skill_level JSON,
			weak_points JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS interview_records (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(128) NOT NULL,
			session_id VARCHAR(128) NOT NULL UNIQUE,
			position VARCHAR(256) DEFAULT '',
			overall_score DOUBLE DEFAULT 0,
			report_json MEDIUMTEXT,
			review_plan_json MEDIUMTEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id),
			INDEX idx_session_id (session_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS sessions (
			session_id VARCHAR(128) PRIMARY KEY,
			user_id VARCHAR(128) NOT NULL,
			session_data MEDIUMTEXT,
			status VARCHAR(32) DEFAULT 'init',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}
	return nil
}

// GetDB 返回底层数据库连接（供其他模块复用，如 auth）
func (s *MySQLStore) GetDB() *sql.DB {
	return s.db
}

// SaveProfile 保存用户画像到 MySQL
func (s *MySQLStore) SaveProfile(ctx context.Context, profile *UserProfile) error {
	skillJSON, _ := json.Marshal(profile.SkillLevel)
	weakJSON, _ := json.Marshal(profile.WeakPoints)

	query := `INSERT INTO user_profiles (user_id, name, skill_level, weak_points, updated_at)
		VALUES (?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			skill_level = VALUES(skill_level),
			weak_points = VALUES(weak_points),
			updated_at = NOW()`

	_, err := s.db.ExecContext(ctx, query, profile.UserID, profile.Name, skillJSON, weakJSON)
	if err != nil {
		return fmt.Errorf("mysql: save profile: %w", err)
	}
	return nil
}

// LoadProfile 从 MySQL 加载用户画像
func (s *MySQLStore) LoadProfile(ctx context.Context, userID string) (*UserProfile, error) {
	query := `SELECT user_id, name, skill_level, weak_points, updated_at FROM user_profiles WHERE user_id = ?`

	var (
		profile  UserProfile
		skillRaw []byte
		weakRaw  []byte
	)

	err := s.db.QueryRowContext(ctx, query, userID).Scan(
		&profile.UserID, &profile.Name, &skillRaw, &weakRaw, &profile.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mysql: load profile: %w", err)
	}

	if len(skillRaw) > 0 {
		_ = json.Unmarshal(skillRaw, &profile.SkillLevel)
	}
	if profile.SkillLevel == nil {
		profile.SkillLevel = make(map[string]string)
	}
	if len(weakRaw) > 0 {
		_ = json.Unmarshal(weakRaw, &profile.WeakPoints)
	}

	// 从兼容表加载训练历史
	histQuery := `SELECT session_id, position, overall_score, created_at
		FROM interview_records WHERE user_id = ? ORDER BY created_at DESC LIMIT 20`
	rows, err := s.db.QueryContext(ctx, histQuery, userID)
	if err != nil {
		return &profile, nil // 画像有了，历史查不到也行
	}
	defer rows.Close()

	for rows.Next() {
		var rec InterviewRecord
		if err := rows.Scan(&rec.SessionID, &rec.LearningGoal, &rec.OverallScore, &rec.Date); err != nil {
			continue
		}
		profile.InterviewHist = append(profile.InterviewHist, rec)
	}

	return &profile, nil
}

// SaveAbilityProfile 使用既有 sessions 数据载体保存完整知识掌握画像，不新增数据库字段。
func (s *MySQLStore) SaveAbilityProfile(ctx context.Context, profile *imodel.StudentAbilityProfile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("mysql: marshal ability profile: %w", err)
	}
	return s.SaveSession(ctx, abilityProfileKey(profile.StudentID), data, 0)
}

// LoadAbilityProfile 从既有 sessions 数据载体读取完整知识掌握画像。
func (s *MySQLStore) LoadAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error) {
	data, err := s.LoadSession(ctx, abilityProfileKey(studentID))
	if err != nil || len(data) == 0 {
		return nil, err
	}
	profile := &imodel.StudentAbilityProfile{}
	if err := json.Unmarshal(data, profile); err != nil {
		return nil, fmt.Errorf("mysql: decode ability profile: %w", err)
	}
	return profile, nil
}

// SaveInterviewRecord 保存训练记录；函数名与表名保留历史兼容。
func (s *MySQLStore) SaveInterviewRecord(ctx context.Context, userID string, record InterviewRecord, reportJSON, reviewPlanJSON string) error {
	query := `INSERT INTO interview_records (user_id, session_id, position, overall_score, report_json, review_plan_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			overall_score = VALUES(overall_score),
			report_json = VALUES(report_json),
			review_plan_json = VALUES(review_plan_json)`

	_, err := s.db.ExecContext(ctx, query, userID, record.SessionID, record.LearningGoal, record.OverallScore, reportJSON, reviewPlanJSON)
	if err != nil {
		return fmt.Errorf("mysql: save training record: %w", err)
	}
	return nil
}

// LoadLatestEvaluationReport 读取学生最近一次已有评价 JSON，不改变数据库结构。
func (s *MySQLStore) LoadLatestEvaluationReport(ctx context.Context, userID string) (*imodel.EvaluationReport, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query := `SELECT report_json FROM interview_records
		WHERE user_id = ? AND report_json IS NOT NULL AND report_json <> ''
		ORDER BY created_at DESC LIMIT 1`
	var reportJSON string
	if err := s.db.QueryRowContext(ctx, query, userID).Scan(&reportJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("mysql: load latest evaluation report: %w", err)
	}
	report := &imodel.EvaluationReport{}
	if err := json.Unmarshal([]byte(reportJSON), report); err != nil {
		return nil, fmt.Errorf("mysql: decode latest evaluation report: %w", err)
	}
	return report, nil
}

// SaveSession 保存会话数据到 MySQL（实现 Store 接口）
func (s *MySQLStore) SaveSession(ctx context.Context, sessionID string, data []byte, _ time.Duration) error {
	query := `INSERT INTO sessions (session_id, user_id, session_data, updated_at)
		VALUES (?, '', ?, NOW())
		ON DUPLICATE KEY UPDATE session_data = VALUES(session_data), updated_at = NOW()`

	_, err := s.db.ExecContext(ctx, query, sessionID, string(data))
	if err != nil {
		return fmt.Errorf("mysql: save session: %w", err)
	}
	return nil
}

// LoadSession 从 MySQL 加载会话数据（实现 Store 接口）
func (s *MySQLStore) LoadSession(ctx context.Context, sessionID string) ([]byte, error) {
	query := `SELECT session_data FROM sessions WHERE session_id = ?`

	var data string
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mysql: load session: %w", err)
	}
	return []byte(data), nil
}

// Close 关闭数据库连接
func (s *MySQLStore) Close() error {
	return s.db.Close()
}
