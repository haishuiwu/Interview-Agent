/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package agent

import "strings"

// Intent 用户意图类型
type Intent string

const (
	IntentStartInterview Intent = "start_interview" // 开始面试
	IntentUploadResume   Intent = "upload_resume"   // 上传简历
	IntentUploadJD       Intent = "upload_jd"       // 上传/输入 JD
	IntentViewHistory    Intent = "view_history"    // 查看历史
	IntentSkill          Intent = "skill"           // 触发 Skill 技能
	IntentChat           Intent = "chat"            // 日常聊天
)

// IntentRouter 意图路由器，基于规则判断用户意图
// 不使用 LLM 做意图识别，用简单规则 + 系统状态即可，既快又准
type IntentRouter struct{}

// NewIntentRouter 创建意图路由器
func NewIntentRouter() *IntentRouter {
	return &IntentRouter{}
}

// interviewTriggers 触发面试的关键词
var interviewTriggers = []string{
	"开始训练", "教学训练", "试讲训练", "教师面试", "结构化问答", "教学答辩",
	"开始面试", "模拟面试", "开始模拟", "面试一下",
	"start interview", "mock interview",
	"我要面试", "开始吧", "来面试",
}

// resumeTriggers 上传简历的关键词
var resumeTriggers = []string{
	"教学档案", "教师档案", "授课经历", "简历", "resume", "我的简历",
}

// jdTriggers 输入 JD 的关键词
var jdTriggers = []string{
	"考核标准", "面试大纲", "课程标准", "jd", "JD", "教师岗位", "岗位", "职位", "招聘",
}

// historyTriggers 查看历史的关键词
var historyTriggers = []string{
	"历史", "记录", "上次训练", "训练记录", "上次面试", "面试记录", "history",
}

// Route 根据用户输入和系统状态判断意图
// isInterviewing: 当前是否在面试中（如果是，所有输入都当作面试回答）
func (r *IntentRouter) Route(input string, isInterviewing bool) Intent {
	// 面试进行中：所有输入都走面试流程
	if isInterviewing {
		return IntentStartInterview
	}

	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)

	// 检查是否是文件路径（上传简历/JD）
	if isFilePath(trimmed) {
		if containsAny(lower, resumeTriggers) {
			return IntentUploadResume
		}
		// 文件默认当简历处理
		return IntentUploadResume
	}

	// 检查是否是 URL（JD 链接）
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return IntentUploadJD
	}

	// 关键词匹配
	if containsAny(lower, interviewTriggers) {
		return IntentStartInterview
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
