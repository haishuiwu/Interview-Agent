/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Mastery Diagnosis
 */

package agent

import "strings"

// Intent 用户意图类型
type Intent string

const (
	IntentStartTraining         Intent = "start_training"          // 开始训练
	IntentUploadStudentProfile  Intent = "upload_student_profile"  // 上传学生画像
	IntentUploadAbilityStandard Intent = "upload_ability_standard" // 上传/输入能力标准
	IntentViewHistory           Intent = "view_history"            // 查看历史
	IntentSkill                 Intent = "skill"                   // 触发 Skill 技能
	IntentChat                  Intent = "chat"                    // 日常聊天
)

// IntentRouter 意图路由器，基于规则判断用户意图
// 不使用 LLM 做意图识别，用简单规则 + 系统状态即可，既快又准
type IntentRouter struct{}

// NewIntentRouter 创建意图路由器
func NewIntentRouter() *IntentRouter {
	return &IntentRouter{}
}

// trainingTriggers 触发能力训练的关键词。
var trainingTriggers = []string{ // 保留部分旧意图短语，仅用于兼容历史输入
	"开始训练", "能力训练", "学习训练", "专项训练", "开始练习",
}

// legacyTrainingTriggers 只兼容历史自然语言入口。
var legacyTrainingTriggers = []string{
	"教学训练", "试讲训练", "教师面试", "结构化问答", "教学答辩",
	"开始面试", "模拟面试", "开始模拟", "面试一下",
	"start interview", "mock interview",
	"我要面试", "开始吧", "来面试",
}

// studentProfileTriggers 识别学生画像文件。
var studentProfileTriggers = []string{
	"学生画像", "学习档案", "学习经历", "能力画像", "项目作品",
}

// legacyStudentProfileTriggers 只兼容历史自然语言入口。
var legacyStudentProfileTriggers = []string{
	"教学档案", "教师档案", "授课经历", "简历", "resume", "我的简历",
}

// historyTriggers 查看历史的关键词
var historyTriggers = []string{
	"历史", "记录", "上次训练", "训练记录", "上次面试", "面试记录", "history",
}

// Route 根据用户输入和系统状态判断意图
// isTraining: 当前是否在训练中（如果是，所有输入都当作训练回答）
func (r *IntentRouter) Route(input string, isTraining bool) Intent {
	// 训练进行中：所有输入都走训练流程
	if isTraining {
		return IntentStartTraining
	}

	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)

	// 检查是否是学生画像文件路径。
	if isFilePath(trimmed) {
		if containsAny(lower, studentProfileTriggers) || containsAny(lower, legacyStudentProfileTriggers) {
			return IntentUploadStudentProfile
		}
		// 保持原有行为：未标注用途的文件默认作为学生画像。
		return IntentUploadStudentProfile
	}

	// URL 默认作为能力标准来源。
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return IntentUploadAbilityStandard
	}

	// 关键词匹配
	if containsAny(lower, trainingTriggers) || containsAny(lower, legacyTrainingTriggers) {
		return IntentStartTraining
	}
	if containsAny(lower, historyTriggers) {
		return IntentViewHistory
	}

	// 默认走聊天
	return IntentChat
}

// containsAny 检查文本是否包含关键词列表中的任意一个
func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// isFilePath 判断输入是否像文件路径
func isFilePath(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "~/") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.HasSuffix(lower, ".pdf") || strings.HasSuffix(lower, ".docx") ||
		strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".md")
}
