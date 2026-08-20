/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package model 定义学生能力提升系统的核心数据模型。
package model

import (
	"strings"
	"time"
)

// ============================================================
// 学习目标与能力标准分析相关
// ============================================================

// AbilityStandard 是从学习目标、课程标准或能力要求中提取的结构化标准。
type AbilityStandard struct {
	RawText              string            `json:"raw_text"`              // 原始能力标准文本
	LearningGoal         string            `json:"learning_goal"`         // 本轮学习目标
	Grade                string            `json:"grade,omitempty"`       // 学段或年级
	Subject              string            `json:"subject,omitempty"`     // 学科或学习领域
	StandardSource       string            `json:"standard_source"`       // 标准来源（如课程、考试或培养方案）
	TargetAbilities      []Ability         `json:"target_abilities"`      // 本轮目标能力
	ExtensionAbilities   []Ability         `json:"extension_abilities"`   // 进阶能力
	ProficiencyLevel     string            `json:"proficiency_level"`     // 当前目标层级
	LearningRequirements []string          `json:"learning_requirements"` // 学习与实践要求
	KeyTopics            []string          `json:"key_topics"`            // 重点训练方向
	Extra                map[string]string `json:"extra,omitempty"`       // 扩展字段
}

// Ability 能力标准中的单项能力。
type Ability struct {
	Name       string `json:"name"`       // 技能名称
	Category   string `json:"category"`   // 能力分类
	Importance string `json:"importance"` // 重要性（must/preferred）
}

// ============================================================
// 学生画像相关
// ============================================================

// StudentProfile 描述学生已有基础、学习经历与能力证据。
type StudentProfile struct {
	StudentID           string               `json:"student_id"`
	Name                string               `json:"name"`
	Grade               string               `json:"grade,omitempty"`
	Subject             string               `json:"subject,omitempty"`
	LearningGoal        string               `json:"learning_goal,omitempty"`
	RawText             string               `json:"raw_text"`
	Education           []Education          `json:"education,omitempty"`
	LearningExperiences []LearningExperience `json:"learning_experiences,omitempty"`
	LearningProjects    []LearningProject    `json:"learning_projects,omitempty"`
	TargetAbilities     []string             `json:"target_abilities,omitempty"`
	Strengths           []string             `json:"strengths,omitempty"`
	Weaknesses          []string             `json:"weaknesses,omitempty"`
	AbilityScores       map[string]float64   `json:"ability_scores,omitempty"`
}

// Education 教育经历
type Education struct {
	School string `json:"school"`
	Degree string `json:"degree"` // bachelor/master/phd
	Major  string `json:"major"`
	Year   string `json:"year"`
}

// LearningExperience 学生的学习或实践经历。
type LearningExperience struct {
	Organization string   `json:"organization,omitempty"`
	Activity     string   `json:"activity"`
	Duration     string   `json:"duration,omitempty"`
	Description  string   `json:"description,omitempty"`
	Abilities    []string `json:"abilities,omitempty"`
}

// LearningProject 学生的学习项目或作品。
type LearningProject struct {
	Name         string   `json:"name"`
	Contribution string   `json:"contribution,omitempty"`
	Description  string   `json:"description,omitempty"`
	Abilities    []string `json:"abilities,omitempty"`
	Highlights   []string `json:"highlights,omitempty"`
}

// ============================================================
// 学生画像与能力标准诊断相关
// ============================================================

// LearningDiagnosis 诊断学生当前能力证据与目标能力标准之间的差距。
type LearningDiagnosis struct {
	StudentID          string              `json:"student_id"`
	LearningGoal       string              `json:"learning_goal"`
	OverallScore       float64             `json:"overall_score"`       // 当前能力证据覆盖度（0-100）
	AbilityAssessments []AbilityAssessment `json:"ability_assessments"` // 单项能力诊断
	AbilityScores      map[string]float64  `json:"ability_scores"`      // 各能力当前得分
	Strengths          []string            `json:"strengths"`           // 已有优势
	Weaknesses         []string            `json:"weaknesses"`          // 待提升能力
	FocusAreas         []string            `json:"focus_areas"`         // 后续训练重点
	EvidenceGaps       []string            `json:"evidence_gaps"`       // 需要训练验证的证据缺口
}

// AbilityAssessment 单项目标能力诊断。
type AbilityAssessment struct {
	AbilityName  string  `json:"ability_name"`
	Target       bool    `json:"target"`
	Demonstrated bool    `json:"demonstrated"`
	Score        float64 `json:"score"`
	Evidence     string  `json:"evidence"`
}

// ============================================================
// 出题规划相关
// ============================================================

const (
	QuestionTypeTheory   = "theory"   // 知识理解与原理
	QuestionTypePractice = "practice" // 实践应用
	QuestionTypeScenario = "scenario" // 综合情境与问题解决
)

// NormalizeQuestionType 将历史题型映射到能力训练题型，兼容已有题库数据。
func NormalizeQuestionType(questionType string) string {
	switch questionType {
	case "basic", "algorithm", QuestionTypeTheory:
		return QuestionTypeTheory
	case "experience", "project", QuestionTypePractice:
		return QuestionTypePractice
	case "design", QuestionTypeScenario:
		return QuestionTypeScenario
	default:
		return questionType
	}
}

// QuestionDirection 训练问题方向（Phase 1 输出）
type QuestionDirection struct {
	Topic       string   `json:"topic"`             // 出题方向/考点（如"sync.Map并发安全"）
	Type        string   `json:"type"`              // 类型（theory/practice/scenario）
	Difficulty  string   `json:"difficulty"`        // 难度（easy/medium/hard）
	SearchQuery string   `json:"search_query"`      // 用于题库检索的关键词
	Skills      []string `json:"skills"`            // 考察技能点
	Context     string   `json:"context,omitempty"` // 学生画像中的相关上下文（practice 类按需填写）
}

// QuestionDirectionPlan Phase 1 输出：出题方向规划
type QuestionDirectionPlan struct {
	Directions []QuestionDirection `json:"directions"`
}

// QuestionPlan 出题计划
type QuestionPlan struct {
	TotalQuestions int               `json:"total_questions"` // 计划出题总数
	Distribution   QuestionDistrib   `json:"distribution"`    // 题目分布
	Questions      []PlannedQuestion `json:"questions"`       // 规划的题目列表
}

// QuestionDistrib 能力训练题型分布。
type QuestionDistrib struct {
	Theory   int `json:"theory"`   // 知识理解题数
	Practice int `json:"practice"` // 实践应用题数
	Scenario int `json:"scenario"` // 综合情境题数
}

// PlannedQuestion 规划的能力训练题。
type PlannedQuestion struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`    // 题目内容
	Type       string   `json:"type"`       // 类型（theory/practice/scenario）
	Difficulty string   `json:"difficulty"` // 难度（easy/medium/hard）
	Skills     []string `json:"skills"`     // 考察技能点
	FollowUps  []string `json:"follow_ups"` // 预设追问
	Reference  string   `json:"reference"`  // 参考答案要点
	Source     string   `json:"source"`     // 来源：题库原题ID 或 "llm"
}

// ============================================================
// 能力训练过程相关
// ============================================================

// DifficultyLevel 难度等级
type DifficultyLevel string

const (
	DifficultyEasy   DifficultyLevel = "easy"
	DifficultyMedium DifficultyLevel = "medium"
	DifficultyHard   DifficultyLevel = "hard"
)

const (
	AbilityLogicalThinking  = "logical_thinking"
	AbilityCommunication    = "communication"
	AbilityProblemSolving   = "problem_solving"
	AbilityCriticalThinking = "critical_thinking"
	AbilityReflection       = "reflection"
)

// CoreAbilityDimensions 返回与现有五个训练 Skill 对齐的能力维度。
func CoreAbilityDimensions() []string {
	return []string{
		AbilityLogicalThinking,
		AbilityCommunication,
		AbilityProblemSolving,
		AbilityCriticalThinking,
		AbilityReflection,
	}
}

// NormalizeAbilityDimension 将 Skill 名称或自然语言能力名映射为核心能力维度。
func NormalizeAbilityDimension(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch {
	case normalized == AbilityCriticalThinking || strings.Contains(normalized, "批判") || strings.Contains(normalized, "证据意识") || strings.Contains(normalized, "假设识别") || strings.Contains(normalized, "结论边界"):
		return AbilityCriticalThinking
	case normalized == AbilityLogicalThinking || strings.Contains(normalized, "逻辑") || strings.Contains(normalized, "推理") || strings.Contains(normalized, "论证") || strings.Contains(normalized, "结构完整") || strings.Contains(normalized, "前后一致"):
		return AbilityLogicalThinking
	case normalized == AbilityCommunication || normalized == "communication_training" || strings.Contains(normalized, "表达") || strings.Contains(normalized, "沟通") || strings.Contains(normalized, "交流") || strings.Contains(normalized, "对象意识") || strings.Contains(normalized, "回应与互动"):
		return AbilityCommunication
	case normalized == AbilityReflection || strings.Contains(normalized, "反思") || strings.Contains(normalized, "复盘") || strings.Contains(normalized, "自我觉察") || strings.Contains(normalized, "迁移意识") || strings.Contains(normalized, "策略调整"):
		return AbilityReflection
	case normalized == AbilityProblemSolving || strings.Contains(normalized, "问题解决") || strings.Contains(normalized, "解决问题") || strings.Contains(normalized, "问题界定") || strings.Contains(normalized, "策略选择") || strings.Contains(normalized, "执行规划") || strings.Contains(normalized, "结果验证") || strings.Contains(normalized, "理解与应用"):
		return AbilityProblemSolving
	default:
		return ""
	}
}

// AbilitySkillName 返回能力维度对应的现有 Skill 名称。
func AbilitySkillName(ability string) string {
	switch NormalizeAbilityDimension(ability) {
	case AbilityLogicalThinking:
		return "logical-thinking"
	case AbilityCommunication:
		return "communication-training"
	case AbilityCriticalThinking:
		return "critical-thinking"
	case AbilityReflection:
		return "reflection-training"
	default:
		return "problem-solving"
	}
}

// AbilityGrowthRecord 记录一次训练前后的可追溯能力变化。
type AbilityGrowthRecord struct {
	SessionID    string             `json:"session_id"`
	LearningGoal string             `json:"learning_goal"`
	BeforeScores map[string]float64 `json:"before_scores,omitempty"`
	AfterScores  map[string]float64 `json:"after_scores,omitempty"`
	ScoreChanges map[string]float64 `json:"score_changes,omitempty"`
	OverallScore float64            `json:"overall_score"`
	TrainingTime time.Time          `json:"training_time"`
}

// StudentAbilityProfile 是跨训练持续维护的结构化学生能力画像，分数范围为 0 到 1。
type StudentAbilityProfile struct {
	StudentID        string                `json:"student_id"`
	Summary          string                `json:"summary"`
	AbilityScores    map[string]float64    `json:"ability_scores,omitempty"`
	Strengths        []string              `json:"strengths,omitempty"`
	Weaknesses       []string              `json:"weaknesses,omitempty"`
	GrowthHistory    []AbilityGrowthRecord `json:"growth_history,omitempty"`
	LastTrainingTime time.Time             `json:"last_training_time,omitempty"`
}

// TrainingState 能力训练过程状态。
type TrainingState struct {
	SessionID             string                 `json:"session_id"`
	CurrentQuestion       int                    `json:"current_question"`                  // 当前第几题
	TotalQuestions        int                    `json:"total_questions"`                   // 总题数
	CurrentDifficulty     DifficultyLevel        `json:"current_difficulty"`                // 当前难度
	ConsecutiveRight      int                    `json:"consecutive_right"`                 // 连续答对
	ConsecutiveWrong      int                    `json:"consecutive_wrong"`                 // 连续答错
	QAHistory             []QAPair               `json:"qa_history"`                        // 问答历史
	StudentAbilityProfile *StudentAbilityProfile `json:"student_ability_profile,omitempty"` // 训练中动态更新的学生能力画像
}

// QAPair 单次问答记录
type QAPair struct {
	Question     PlannedQuestion `json:"question"`
	UserAnswer   string          `json:"user_answer"`
	Score        float64         `json:"score"`          // 本题得分（0-100）
	Feedback     string          `json:"feedback"`       // 即时反馈
	FollowUpUsed bool            `json:"follow_up_used"` // 是否进行了追问
}

// ============================================================
// 评估报告相关
// ============================================================

// EvaluationReport 学生能力训练评估报告。
type EvaluationReport struct {
	SessionID       string             `json:"session_id"`
	StudentID       string             `json:"student_id"`
	StudentName     string             `json:"student_name"`
	Grade           string             `json:"grade,omitempty"`
	Subject         string             `json:"subject,omitempty"`
	LearningGoal    string             `json:"learning_goal"`
	OverallScore    float64            `json:"overall_score"`    // 综合得分
	OverallLevel    string             `json:"overall_level"`    // 综合评级（A/B/C/D）
	AbilityScores   map[string]float64 `json:"ability_scores"`   // 各能力维度得分
	TrainingMetrics map[string]float64 `json:"training_metrics"` // 由会话事实确定性计算的训练指标
	Strengths       []string           `json:"strengths"`        // 表现优秀的方面
	Weaknesses      []string           `json:"weaknesses"`       // 需要提升的方面
	DetailedReview  []QuestionReview   `json:"detailed_review"`  // 逐题点评
	Summary         string             `json:"summary"`          // 综合评语
	CreatedAt       time.Time          `json:"created_at"`
}

// QuestionReview 单题点评
type QuestionReview struct {
	QuestionContent string   `json:"question_content"`
	UserAnswer      string   `json:"user_answer"`
	Score           float64  `json:"score"`
	Comment         string   `json:"comment"`
	KeyPointsHit    []string `json:"key_points_hit"`    // 命中的知识点
	KeyPointsMissed []string `json:"key_points_missed"` // 遗漏的知识点
}

// ============================================================
// 学生能力提升计划相关
// ============================================================

// ReviewPlan 学生能力提升计划
type ReviewPlan struct {
	SessionID string      `json:"session_id"`
	WeakAreas []WeakArea  `json:"weak_areas"` // 薄弱领域
	StudyPlan []StudyItem `json:"study_plan"` // 学习计划
	Resources []Resource  `json:"resources"`  // 推荐资源
	CreatedAt time.Time   `json:"created_at"`
}

// WeakArea 薄弱领域
type WeakArea struct {
	Topic    string  `json:"topic"`
	Score    float64 `json:"score"`    // 该领域得分
	Priority string  `json:"priority"` // 优先级（high/medium/low）
}

// StudyItem 学习项
type StudyItem struct {
	Topic        string   `json:"topic"`
	Objective    string   `json:"objective"`     // 学习目标
	Actions      []string `json:"actions"`       // 具体行动
	TimeEstimate string   `json:"time_estimate"` // 预估时间
}

// Resource 推荐资源
type Resource struct {
	Title string `json:"title"`
	Type  string `json:"type"` // article/video/repo/book
	URL   string `json:"url"`
	Desc  string `json:"desc"`
}

// ============================================================
// 会话管理
// ============================================================

// Session 能力训练会话。
type Session struct {
	ID                string             `json:"id"`
	UserID            string             `json:"user_id"`
	StudentID         string             `json:"student_id"`
	AbilityStandard   *AbilityStandard   `json:"ability_standard"`
	StudentProfile    *StudentProfile    `json:"student_profile"`
	LearningDiagnosis *LearningDiagnosis `json:"learning_diagnosis"`
	QuestionPlan      *QuestionPlan      `json:"question_plan"`
	TrainingState     *TrainingState     `json:"training_state"`
	Report            *EvaluationReport  `json:"report"`
	ReviewPlan        *ReviewPlan        `json:"review_plan"`
	Status            SessionStatus      `json:"status"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// SessionStatus 会话状态
type SessionStatus string

const (
	StatusInit                   SessionStatus = "init"                     // 初始化
	StatusAbilityAnalyzed        SessionStatus = "ability_analyzed"         // 能力标准已分析
	StatusStudentProfileAnalyzed SessionStatus = "student_profile_analyzed" // 学生画像已诊断
	StatusPlanned                SessionStatus = "planned"                  // 已规划训练
	StatusTraining               SessionStatus = "training"                 // 训练中
	StatusTerminated             SessionStatus = "terminated"               // 用户主动终止
	StatusEvaluated              SessionStatus = "evaluated"                // 已评估
	StatusCompleted              SessionStatus = "completed"                // 已完成
)
