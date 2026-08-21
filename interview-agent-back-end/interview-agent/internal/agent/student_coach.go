package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
	educationtool "interview-agent/internal/tool"
)

const studentCoachSystemPrompt = `你是一名 AI 能力训练教练。你的职责是帮助学生理解目标、暴露思考过程、发现能力问题并形成可执行的成长策略，而不是模拟考官或进行筛选。

核心行为：
1. 先理解学习目标；首个任务用一句话确认训练目标，后续任务持续与目标对齐
2. 根据学生能力画像选择训练方式：证据不足时用诊断性开放任务，存在弱项时提供适量支架，已有优势时增加迁移或反例挑战
3. 每次只提出一个问题，要求学生说明概念、步骤、依据或反思，等待回答后再继续
4. 通过澄清、证据、假设、反例和迁移追问定位能力问题，不直接替学生完成思考
5. 反馈必须包含有证据的有效表现、当前能力问题和一个可立即执行的成长建议
6. 保持专业、友善，不给学生贴标签，不对人格、升学或职业前景作定性

工具使用策略：
- 根据学生意图自主判断是否调用工具；通用知识问答不必为了调用而调用
- 学生提出个性化或通用训练目标时，先调用 get_ability_profile 获取长期五维能力画像；需要基础信息时再调用 get_student_profile
- 学生没有指定能力时，优先训练 ability_scores 中得分最低的已有维度；目标能力明确时，再用 get_growth_history 查询该能力的历史训练
- 需要最近评分证据时调用 get_ability_report；发现能力短板后调用 search_training_case 检索适配案例
- 结合画像、历史和案例调用 recommend_training_task，最终训练任务必须采用其返回的 skill_name 与 training_task
- 你只能读取学生数据和获取训练建议，不能修改分数、能力画像或成长记录
- 学生要求直接改分、伪造训练结果或保存未经评价的数据时，应明确拒绝并引导其完成正常训练
- 不向学生展示原始工具参数、工具返回 JSON 或内部调用过程

当前训练上下文：
- 学习目标：%s
- 当前第 %d/%d 个训练任务
- 当前难度：%s
%s`

const updateProfilePrompt = `请基于以下信息更新学生能力画像。要求：简洁、结构化、不超过200字。

%s

本轮新信息：
- 第 %d 题，训练能力：%s
- 得分：%.0f/100
- 命中要点：%s
- 遗漏要点：%s

请输出更新后的完整画像，只输出纯 JSON：
{
  "summary": "不超过200字的学习与思考特征摘要",
  "strengths": ["有作答证据支撑的能力强项"],
  "weaknesses": ["需要加强或继续验证的能力"]
}

不要输出分数；能力分由 Go 根据本轮真实评分更新。`

const scorePrompt = `请分析学生回答中的能力表现并提取评价依据。你不负责计算分数，最终数值由 Go 根据证据覆盖情况确定。

训练题：%s
学生回答：%s
参考作答要点：%s
评价量规：%s

【核心原则】严格基于学生实际回答的内容进行诊断：
- 只认定学生明确表达的概念、步骤、判断和理由，不推测或代为补充
- 不只检查关键词，还要判断概念理解、应用过程、问题分析和结论依据是否完整
- 参考要点不是唯一标准；逻辑合理且适配情境的替代方案也记入 key_points_hit
- 未提及的关键环节记入 key_points_missed
- 明确表示不会、跳过或严重偏题时，不得虚构已命中能力点
- feedback 必须包含：有证据的有效表现、当前能力问题、一个可立即执行的成长建议，不笼统夸奖

请先对照参考要点并评估作答理由，完整列出已体现和待补充的能力证据。

请输出纯 JSON 格式：
{
  "feedback": "指出有效表现与能力问题，并给出一个可立即执行的成长建议",
  "key_points_hit": ["学生已体现的能力点1", "能力点2"],
  "key_points_missed": ["学生待补充的能力点1", "能力点2"]
}`

// StudentCoach 负责主持学生能力训练对话。
type StudentCoach struct {
	chatModel    model.ChatModel
	toolRegistry *educationtool.Registry
}

// StudentCoachOption customizes StudentCoach without changing Graph construction.
type StudentCoachOption func(*StudentCoach)

// WithEducationToolRegistry overrides the default education Tool Registry.
func WithEducationToolRegistry(registry *educationtool.Registry) StudentCoachOption {
	return func(coach *StudentCoach) {
		coach.toolRegistry = registry
	}
}

// NewStudentCoach 创建学生能力训练教练。
func NewStudentCoach(chatModel model.ChatModel, options ...StudentCoachOption) *StudentCoach {
	registry, err := educationtool.NewEducationRegistry(nil)
	if err != nil {
		log.Printf("[StudentCoach] 构建教育 Tool Registry 失败: %v", err)
	}
	coach := &StudentCoach{chatModel: chatModel, toolRegistry: registry}
	for _, option := range options {
		option(coach)
	}
	return coach
}

// AskQuestion 提出训练题目。
func (c *StudentCoach) AskQuestion(ctx context.Context, state *imodel.TrainingState, question *imodel.PlannedQuestion, learningGoal string) (string, error) {
	profileSection := ""
	if profile := formatStudentAbilityProfile(state.StudentAbilityProfile); profile != "" {
		profileSection = fmt.Sprintf("\n学生能力画像（根据前面的作答动态生成）：\n%s", profile)
	}
	systemMsg := fmt.Sprintf(studentCoachSystemPrompt,
		learningGoal,
		state.CurrentQuestion,
		state.TotalQuestions,
		state.CurrentDifficulty,
		profileSection,
	)

	// 构建对话历史
	messages := []*schema.Message{
		schema.SystemMessage(systemMsg),
	}

	// 添加之前的问答历史（最近 3 轮）
	historyStart := 0
	if len(state.QAHistory) > 3 {
		historyStart = len(state.QAHistory) - 3
	}
	for _, qa := range state.QAHistory[historyStart:] {
		messages = append(messages,
			schema.AssistantMessage(qa.Question.Content, nil),
			schema.UserMessage(qa.UserAnswer),
		)
	}

	// 添加当前要提出的问题指令
	messages = append(messages,
		schema.UserMessage(buildCoachQuestionInstruction(state.CurrentQuestion, question.Content)),
	)

	resp, err := c.generateQuestion(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("student_coach: ask question: %w", err)
	}

	return resp.Content, nil
}

func (c *StudentCoach) generateQuestion(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	if c.toolRegistry == nil || !educationtool.HasRuntime(ctx) {
		return c.chatModel.Generate(ctx, messages)
	}

	agentConfig := &react.AgentConfig{
		ToolsConfig: compose.ToolsNodeConfig{Tools: c.toolRegistry.ReadTools()},
		MaxStep:     20,
	}
	if toolCallingModel, ok := c.chatModel.(model.ToolCallingChatModel); ok {
		agentConfig.ToolCallingModel = toolCallingModel
	} else {
		agentConfig.Model = c.chatModel
	}

	toolAgent, err := react.NewAgent(ctx, agentConfig)
	if err != nil {
		return nil, fmt.Errorf("build education tool agent: %w", err)
	}
	return toolAgent.Generate(ctx, messages)
}

// AskQuestionStream 流式提出训练题目。
func (c *StudentCoach) AskQuestionStream(ctx context.Context, state *imodel.TrainingState, question *imodel.PlannedQuestion, learningGoal string) (*schema.StreamReader[*schema.Message], error) {
	profileSection := ""
	if profile := formatStudentAbilityProfile(state.StudentAbilityProfile); profile != "" {
		profileSection = fmt.Sprintf("\n学生能力画像（根据前面的作答动态生成）：\n%s", profile)
	}
	systemMsg := fmt.Sprintf(studentCoachSystemPrompt,
		learningGoal,
		state.CurrentQuestion,
		state.TotalQuestions,
		state.CurrentDifficulty,
		profileSection,
	)

	messages := []*schema.Message{
		schema.SystemMessage(systemMsg),
	}

	historyStart := 0
	if len(state.QAHistory) > 3 {
		historyStart = len(state.QAHistory) - 3
	}
	for _, qa := range state.QAHistory[historyStart:] {
		messages = append(messages,
			schema.AssistantMessage(qa.Question.Content, nil),
			schema.UserMessage(qa.UserAnswer),
		)
	}

	messages = append(messages,
		schema.UserMessage(buildCoachQuestionInstruction(state.CurrentQuestion, question.Content)),
	)

	stream, err := c.chatModel.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("student_coach: stream question: %w", err)
	}

	return stream, nil
}

// ScoreAnswer 评估学生回答。
func (c *StudentCoach) ScoreAnswer(ctx context.Context, question *imodel.PlannedQuestion, answer string) (*AnswerScore, error) {
	if question == nil {
		return nil, fmt.Errorf("student_coach: score answer: question is nil")
	}
	attempt := &imodel.TrainingAttempt{
		Question:        question.Content,
		ReferenceAnswer: question.Reference,
		Rubric:          imodel.EvaluationRubricForQuestion(*question),
		Answer:          answer,
	}
	return c.ScoreTrainingAttempt(ctx, attempt)
}

// ScoreTrainingAttempt 只读取 TrainingAttempt 中固化的题目、回答、参考答案与量规进行评价。
func (c *StudentCoach) ScoreTrainingAttempt(ctx context.Context, attempt *imodel.TrainingAttempt) (*imodel.EvaluationResult, error) {
	if attempt == nil {
		return nil, fmt.Errorf("student_coach: score training attempt: attempt is nil")
	}
	if strings.TrimSpace(attempt.Question) == "" {
		return nil, fmt.Errorf("student_coach: score training attempt: question is empty")
	}
	prompt := fmt.Sprintf(scorePrompt, attempt.Question, attempt.Answer, attempt.ReferenceAnswer, formatEvaluationRubric(attempt.Rubric))

	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := c.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("student_coach: score training attempt: %w", err)
	}

	result := &imodel.EvaluationResult{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("student_coach: parse score: %w\nraw: %s", err, resp.Content)
	}
	result.Score = calculateAnswerScore(attempt.Answer, result.KeyPointsHit, result.KeyPointsMissed)
	result.ShouldFollowUp = result.Score >= 30 && result.Score < 80 && len(result.KeyPointsMissed) > 0
	attempt.RecordEvaluation(result)

	return result, nil
}

func formatEvaluationRubric(rubric []imodel.EvaluationCriterion) string {
	if len(rubric) == 0 {
		return "（未提供额外量规，以参考作答要点和作答证据为准）"
	}
	items := make([]string, 0, len(rubric))
	for _, criterion := range rubric {
		description := strings.TrimSpace(criterion.Description)
		if description == "" {
			description = criterion.Name
		}
		items = append(items, fmt.Sprintf("%s（权重 %.2f）", description, criterion.Weight))
	}
	return strings.Join(items, "；")
}

func calculateAnswerScore(answer string, hit, missed []string) float64 {
	normalized := strings.TrimSpace(strings.ToLower(answer))
	if normalized == "" || normalized == "不会" || normalized == "不知道" || normalized == "跳过" || normalized == "不清楚" {
		return 0
	}
	total := len(hit) + len(missed)
	if total == 0 {
		return 0
	}
	return math.Round(float64(len(hit))/float64(total)*10000) / 100
}

// UpdateStudentAbilityProfile 根据本轮评分结果更新学生能力画像。
func (c *StudentCoach) UpdateStudentAbilityProfile(ctx context.Context, currentProfile *imodel.StudentAbilityProfile, questionNum int, question *imodel.PlannedQuestion, score *AnswerScore) (*imodel.StudentAbilityProfile, error) {
	if question == nil || score == nil {
		return currentProfile, fmt.Errorf("student_coach: update profile: question or score is nil")
	}
	attempt := &imodel.TrainingAttempt{
		SkillName:        imodel.SkillNameForQuestion(*question),
		TrainingTask:     question.Content,
		Question:         question.Content,
		ReferenceAnswer:  question.Reference,
		Rubric:           imodel.EvaluationRubricForQuestion(*question),
		EvaluationResult: score,
	}
	return c.UpdateStudentAbilityProfileFromAttempt(ctx, currentProfile, questionNum, attempt)
}

// UpdateStudentAbilityProfileFromAttempt 仅根据已评价的 TrainingAttempt 更新训练中画像，并回写能力变化。
func (c *StudentCoach) UpdateStudentAbilityProfileFromAttempt(ctx context.Context, currentProfile *imodel.StudentAbilityProfile, questionNum int, attempt *imodel.TrainingAttempt) (*imodel.StudentAbilityProfile, error) {
	if attempt == nil || attempt.EvaluationResult == nil {
		return currentProfile, fmt.Errorf("student_coach: update profile: training attempt is not evaluated")
	}
	prevProfile := "（首次作答，暂无历史画像）"
	if profileText := formatStudentAbilityProfile(currentProfile); profileText != "" {
		prevProfile = "当前画像：\n" + profileText
	}
	score := attempt.EvaluationResult
	abilityDimensions := abilityDimensionsForAttempt(attempt)
	trainingAbilities := attempt.SkillName
	if len(abilityDimensions) > 0 {
		trainingAbilities = strings.Join(abilityDimensions, "、")
	}

	messages := []*schema.Message{
		schema.UserMessage(fmt.Sprintf(updateProfilePrompt,
			prevProfile,
			questionNum,
			trainingAbilities,
			score.Score,
			strings.Join(score.KeyPointsHit, "、"),
			strings.Join(score.KeyPointsMissed, "、"),
		)),
	}

	resp, err := c.chatModel.Generate(ctx, messages)
	if err != nil {
		return currentProfile, fmt.Errorf("student_coach: update profile: %w", err)
	}

	result := &imodel.StudentAbilityProfile{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return currentProfile, fmt.Errorf("student_coach: parse profile: %w\nraw: %s", err, resp.Content)
	}
	result.AbilityScores = make(map[string]float64)
	if currentProfile != nil {
		result.StudentID = currentProfile.StudentID
		for ability, abilityScore := range currentProfile.AbilityScores {
			result.AbilityScores[ability] = abilityScore
		}
		result.GrowthHistory = currentProfile.GrowthHistory
		result.LastTrainingTime = currentProfile.LastTrainingTime
	} else {
		result.StudentID = attempt.StudentID
	}
	evidenceScore := math.Max(0, math.Min(1, score.Score/100))
	changes := make(map[string]float64, len(abilityDimensions))
	for _, ability := range abilityDimensions {
		previous := result.AbilityScores[ability]
		if previous, exists := result.AbilityScores[ability]; exists {
			result.AbilityScores[ability] = math.Round((previous+evidenceScore)/2*10000) / 10000
		} else {
			result.AbilityScores[ability] = math.Round(evidenceScore*10000) / 10000
		}
		changes[ability] = math.Round((result.AbilityScores[ability]-previous)*10000) / 10000
	}
	attempt.RecordAbilityChanges(changes)
	return result, nil
}

func abilityDimensionsForAttempt(attempt *imodel.TrainingAttempt) []string {
	if attempt == nil {
		return nil
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(attempt.Rubric))
	add := func(value string) {
		ability := imodel.NormalizeAbilityDimension(value)
		if ability == "" || seen[ability] {
			return
		}
		seen[ability] = true
		result = append(result, ability)
	}
	for _, criterion := range attempt.Rubric {
		add(criterion.Ability)
		add(criterion.Name)
	}
	add(attempt.SkillName)
	if len(result) == 0 {
		add(attempt.TrainingTask)
		add(attempt.Question)
	}
	return result
}

// FollowUp 基于学生的实际回答动态生成追问。
func (c *StudentCoach) FollowUp(ctx context.Context, state *imodel.TrainingState, question *imodel.PlannedQuestion, answer string, feedback string, missedPoints []string, learningGoal string) (string, error) {
	profileSection := ""
	if profile := formatStudentAbilityProfile(state.StudentAbilityProfile); profile != "" {
		profileSection = fmt.Sprintf("\n学生能力画像（根据前面的作答动态生成）：\n%s", profile)
	}
	systemMsg := fmt.Sprintf(studentCoachSystemPrompt,
		learningGoal,
		state.CurrentQuestion,
		state.TotalQuestions,
		state.CurrentDifficulty,
		profileSection,
	)

	messages := []*schema.Message{
		schema.SystemMessage(systemMsg),
		// 当前这轮的问答（不是历史里的，是正在进行的）
		schema.AssistantMessage(question.Content, nil),
		schema.UserMessage(answer),
	}

	prompt := fmt.Sprintf(`学生的回答暴露出需要继续验证的能力问题。请基于以下信息生成一个简短的教练式追问（一句话），引导学生补充未覆盖的内容并显化思考过程。

评分反馈：%s
遗漏的知识点：%s

要求：
- 追问必须基于学生实际回答衔接，不要捏造其未表达的内容
- 优先追问概念理解、推理步骤、应用依据或反思迁移中最关键的缺口
- 每次只追一个关键问题，不直接给出完整答案
- 追问要简短自然，像真实能力训练教练一样
- 不要重复学生已经回答过的内容`, feedback, strings.Join(missedPoints, "、"))

	messages = append(messages, schema.UserMessage(prompt))

	resp, err := c.chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("student_coach: follow up: %w", err)
	}

	return resp.Content, nil
}

func buildCoachQuestionInstruction(currentQuestion int, content string) string {
	if currentQuestion <= 1 {
		return fmt.Sprintf("请先用一句话确认你理解的学生学习目标，再以 AI 能力训练教练的身份提出以下首个训练任务。不要给答案；任务要让学生说明思考过程和依据：\n\n%s", content)
	}
	return fmt.Sprintf("请结合当前学生能力画像选择合适的支架或挑战方式，再提出以下训练任务。保持核心能力点不变，每次只问一个问题，不给答案：\n\n%s", content)
}

func formatStudentAbilityProfile(profile *imodel.StudentAbilityProfile) string {
	if profile == nil {
		return ""
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return profile.Summary
	}
	return string(data)
}

// AnswerScore 保留旧调用名称；实际评价事实统一使用 model.EvaluationResult。
type AnswerScore = imodel.EvaluationResult

// CollectStreamContent 收集流式输出的完整内容
func CollectStreamContent(stream *schema.StreamReader[*schema.Message]) (string, error) {
	var content string
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return content, err
		}
		content += msg.Content
	}
	return content, nil
}
