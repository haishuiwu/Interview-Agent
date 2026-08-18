/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

const interviewerSystemPrompt = `你是一名资深教研员和教师能力训练导师，风格专业、友善且善于启发。你正在主持一场教师教学能力训练。

训练规则：
1. 每次只问一个问题，等待学员回答后再继续
2. 根据作答质量决定是否用苏格拉底式追问引导补充
3. 对有依据的教学决策给予肯定，对不完整的回答提示其补充学情、目标、活动或评价依据
4. 保持专业、友善的语气
5. 不直接给出完整答案，不将训练结果表述为录用结论

当前训练上下文：
- 目标教师岗位或考核：%s
- 当前第 %d/%d 个训练任务
- 当前难度：%s
%s`

const updateProfilePrompt = `请基于以下信息更新学员的教学能力画像。要求：简洁、结构化、不超过200字。

%s

本轮新信息：
- 第 %d 题，训练能力：%s
- 得分：%.0f/100
- 命中要点：%s
- 遗漏要点：%s

请输出更新后的完整画像（纯文本，不要 JSON）。画像应包含：
1. 教学强项（哪些能力有作答证据支撑）
2. 待训练能力（哪些方面需加强或继续验证）
3. 教学思考特征（如：关注学情/偏重知识讲解、善于举例/缺少评价闭环等）`

const scorePrompt = `请对学员的回答进行教学能力诊断和客观评分。

训练题：%s
学员回答：%s
参考作答要点：%s

【核心原则】严格基于学员实际回答的内容进行诊断：
- 只认定学员明确表达的观点、教学步骤和理由，不推测或代为补充
- 不只检查关键词，还要判断教学目标、学情依据、活动设计和评价方式能否形成闭环
- 参考要点不是唯一标准；有合理教育依据且适配情境的替代方案也可以得分
- 未提及的关键环节记入 key_points_missed
- 明确表示不会或跳过，得分应为 0-10 分；严重偏题为 0-20 分
- feedback 要给出具体、可执行的改进提示，不笼统夸奖

请先对照参考要点并评估教学理由，列出已体现和待补充的能力，再根据完整性、合理性和可实施性给分。

请输出纯 JSON 格式：
{
  "score": <0-100的数值，根据下方评分标准和实际命中比例计算>,
  "feedback": "具体指出哪些点答得好、哪些点遗漏了",
  "key_points_hit": ["学员已体现的能力点1", "能力点2"],
  "key_points_missed": ["学员待补充的能力点1", "能力点2"],
  "should_follow_up": true
}

评分标准：
- 90-100：方案完整、理由充分、适配学情且有评价闭环
- 70-89：方案合理并覆盖主要环节，仍有少量可改进点
- 50-69：具备基本教学思路，但关键环节或依据有明显遗漏
- 30-49：只有零散观点，难以形成可实施方案
- 0-29：未能作答、完全偏题或方案存在明显教育风险`

// Interviewer 面试官 Agent，负责主持面试对话
type Interviewer struct {
	chatModel model.ChatModel
}

// NewInterviewer 创建面试官 Agent
func NewInterviewer(chatModel model.ChatModel) *Interviewer {
	return &Interviewer{chatModel: chatModel}
}

// AskQuestion 提出面试题目（支持流式输出）
func (iv *Interviewer) AskQuestion(ctx context.Context, state *imodel.InterviewState, question *imodel.PlannedQuestion, position string) (string, error) {
	profileSection := ""
	if state.CandidateProfile != "" {
		profileSection = fmt.Sprintf("\n学员教学能力画像（根据前面的作答动态生成）：\n%s", state.CandidateProfile)
	}
	systemMsg := fmt.Sprintf(interviewerSystemPrompt,
		position,
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
		schema.UserMessage(fmt.Sprintf("请以教研员的身份直接提出以下训练题，保持简洁，不要加额外铺垫、背景说明或答案提示：\n\n%s", question.Content)),
	)

	resp, err := iv.chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("interviewer: ask question: %w", err)
	}

	return resp.Content, nil
}

// AskQuestionStream 提出面试题目（流式输出）
func (iv *Interviewer) AskQuestionStream(ctx context.Context, state *imodel.InterviewState, question *imodel.PlannedQuestion, position string) (*schema.StreamReader[*schema.Message], error) {
	profileSection := ""
	if state.CandidateProfile != "" {
		profileSection = fmt.Sprintf("\n学员教学能力画像（根据前面的作答动态生成）：\n%s", state.CandidateProfile)
	}
	systemMsg := fmt.Sprintf(interviewerSystemPrompt,
		position,
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
		schema.UserMessage(fmt.Sprintf("请以教研员的身份直接提出以下训练题，保持简洁，不要加额外铺垫、背景说明或答案提示：\n\n%s", question.Content)),
	)

	stream, err := iv.chatModel.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("interviewer: stream question: %w", err)
	}

	return stream, nil
}

// ScoreAnswer 评估候选人的回答
func (iv *Interviewer) ScoreAnswer(ctx context.Context, question *imodel.PlannedQuestion, answer string) (*AnswerScore, error) {
	prompt := fmt.Sprintf(scorePrompt, question.Content, answer, question.Reference)

	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := iv.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("interviewer: score answer: %w", err)
	}

	result := &AnswerScore{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("interviewer: parse score: %w\nraw: %s", err, resp.Content)
	}

	return result, nil
}

// UpdateCandidateProfile 根据本轮评分结果更新候选人动态画像
func (iv *Interviewer) UpdateCandidateProfile(ctx context.Context, currentProfile string, questionNum int, question *imodel.PlannedQuestion, score *AnswerScore) (string, error) {
	prevProfile := "（首次作答，暂无历史画像）"
	if currentProfile != "" {
		prevProfile = "当前画像：\n" + currentProfile
	}

	messages := []*schema.Message{
		schema.UserMessage(fmt.Sprintf(updateProfilePrompt,
			prevProfile,
			questionNum,
			strings.Join(question.Skills, "、"),
			score.Score,
			strings.Join(score.KeyPointsHit, "、"),
			strings.Join(score.KeyPointsMissed, "、"),
		)),
	}

	resp, err := iv.chatModel.Generate(ctx, messages)
	if err != nil {
		return currentProfile, fmt.Errorf("interviewer: update profile: %w", err)
	}

	return resp.Content, nil
}

// FollowUp 基于候选人的实际回答动态生成追问
// question: 当前题目，answer: 候选人的实际回答，feedback: 评分反馈，missedPoints: 遗漏的知识点
func (iv *Interviewer) FollowUp(ctx context.Context, state *imodel.InterviewState, question *imodel.PlannedQuestion, answer string, feedback string, missedPoints []string, position string) (string, error) {
	profileSection := ""
	if state.CandidateProfile != "" {
		profileSection = fmt.Sprintf("\n学员教学能力画像（根据前面的作答动态生成）：\n%s", state.CandidateProfile)
	}
	systemMsg := fmt.Sprintf(interviewerSystemPrompt,
		position,
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

	prompt := fmt.Sprintf(`学员的回答有部分遗漏，请基于以下信息生成一个简短的苏格拉底式追问（一句话），引导学员补充未覆盖的内容。

评分反馈：%s
遗漏的知识点：%s

要求：
- 追问必须基于学员实际回答衔接，不要捏造其未表达的内容
- 优先追问学情依据、教学目标、活动设计或评价闭环中最关键的缺口
- 追问要简短自然，像真实教研员一样
- 不要重复学员已经回答过的内容`, feedback, strings.Join(missedPoints, "、"))

	messages = append(messages, schema.UserMessage(prompt))

	resp, err := iv.chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("interviewer: follow up: %w", err)
	}

	return resp.Content, nil
}

// AnswerScore 回答评分结果
type AnswerScore struct {
	Score           float64  `json:"score"`
	Feedback        string   `json:"feedback"`
	KeyPointsHit    []string `json:"key_points_hit"`
	KeyPointsMissed []string `json:"key_points_missed"`
	ShouldFollowUp  bool     `json:"should_follow_up"`
}

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
