package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/rag"
)

const abilityTrainingStartPrompt = `你是一名 AI 能力训练教练。你的任务不是考核学生，而是先理解学生的目标，再设计能暴露思考过程的训练活动。

Skill 定义：
%s

学生输入：
%s

可用参考材料：
%s

请执行以下行为：
1. 用一句话复述你理解的学生目标；信息不足时说明本轮采用的合理训练焦点，允许学生纠正。
2. 根据训练目标选择一个适合当前学生的训练方式，例如情境分析、观点辨析、方案设计、角色表达或引导复盘。
3. 只给出一个训练任务，要求学生说明思考过程、依据或选择理由。
4. 不直接给答案，不使用考核或筛选口吻。

直接输出给学生的内容，控制在 300 字以内。`

const abilityTrainingTurnPrompt = `你是一名 AI 能力训练教练。请根据学生的真实回答继续训练，不得把训练变成知识问答或结果判定。

Skill 定义：
%s

学生目标：%s
上一轮训练任务：%s
学生回答：%s
此前发现的能力问题：%s

行为要求：
1. 先指出回答中有证据支持的有效表现。
2. 识别一个最值得继续训练的能力问题，说明依据，不给学生贴标签。
3. 给出一个能立即执行的成长建议。
4. %s
5. 按评价维度分别给出 0-100 的训练观察分；证据不足时应保守评分。

只输出纯 JSON：
{
  "feedback": "基于学生回答的具体反馈",
  "ability_issue": "当前最需要训练的能力问题及依据",
  "growth_suggestion": "一个具体、可执行的成长建议",
  "next_question": "下一轮只问一个追问；训练结束时为空字符串",
  "dimension_scores": {"评价维度": 75},
  "summary": "训练结束时给出目标、表现、问题和下一步计划的总结，否则为空字符串"
}`

// AbilityTrainingSkill 是五类学生能力训练 Skill 共享的多轮训练实现。
type AbilityTrainingSkill struct {
	definition  TrainingSkillDefinition
	triggers    []string
	chatModel   model.ChatModel
	milvusStore *rag.MilvusStore
	bm25Manager *rag.BM25Manager
}

type abilityTrainingTurn struct {
	Feedback         string             `json:"feedback"`
	AbilityIssue     string             `json:"ability_issue"`
	GrowthSuggestion string             `json:"growth_suggestion"`
	NextQuestion     string             `json:"next_question"`
	DimensionScores  map[string]float64 `json:"dimension_scores"`
	Summary          string             `json:"summary"`
}

func newAbilityTrainingSkill(
	definition TrainingSkillDefinition,
	triggers []string,
	chatModel model.ChatModel,
	milvusStore *rag.MilvusStore,
	bm25Manager *rag.BM25Manager,
) *AbilityTrainingSkill {
	return &AbilityTrainingSkill{
		definition:  definition,
		triggers:    triggers,
		chatModel:   chatModel,
		milvusStore: milvusStore,
		bm25Manager: bm25Manager,
	}
}

func (s *AbilityTrainingSkill) Name() string { return s.definition.Name }

func (s *AbilityTrainingSkill) Description() string { return s.definition.Description }

func (s *AbilityTrainingSkill) Definition() TrainingSkillDefinition { return s.definition }

func (s *AbilityTrainingSkill) Match(input string) bool {
	return ContainsAny(input, s.triggers)
}

func (s *AbilityTrainingSkill) Handle(ctx context.Context, input string, state *SkillState) (*SkillResponse, error) {
	if state == nil || state.Data == nil || state.Data["phase"] == nil {
		return s.start(ctx, input, state)
	}
	return s.continueTraining(ctx, input, state)
}

func (s *AbilityTrainingSkill) start(ctx context.Context, input string, state *SkillState) (*SkillResponse, error) {
	if state == nil {
		state = NewSkillState(s.Name())
	}
	state.SkillName = s.Name()
	state.Data = make(map[string]interface{})

	goal := strings.TrimSpace(input)
	if goal == "" {
		goal = s.definition.TrainingGoals[0]
	}
	reference := s.fetchRAGContext(ctx, goal, state.UserID)
	if reference == "" {
		reference = "暂无额外参考材料，以学生目标和 Skill 定义为准。"
	}

	prompt := fmt.Sprintf(
		abilityTrainingStartPrompt,
		formatTrainingDefinition(s.definition),
		goal,
		reference,
	)
	resp, err := s.chatModel.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return nil, fmt.Errorf("%s: start training: %w", s.Name(), err)
	}
	task := strings.TrimSpace(resp.Content)
	if task == "" {
		return nil, fmt.Errorf("%s: empty training task", s.Name())
	}

	state.Data["phase"] = "training"
	state.Data["goal"] = goal
	state.Data["last_task"] = task
	state.Data["ability_issues"] = []string{}

	return &SkillResponse{
		Content:    fmt.Sprintf("[%s]\n\n%s", s.definition.Description, task),
		Done:       false,
		NextPrompt: "请说明你的思考过程、判断依据和选择理由（输入 /quit 退出）",
		State:      state,
	}, nil
}

func (s *AbilityTrainingSkill) continueTraining(ctx context.Context, input string, state *SkillState) (*SkillResponse, error) {
	goal, _ := state.Data["goal"].(string)
	lastTask, _ := state.Data["last_task"].(string)
	issues, _ := state.Data["ability_issues"].([]string)

	state.NextRound()
	shouldFinish := state.Round >= 4
	nextAction := "基于能力问题生成一个短追问，改变情境或支架方式，继续验证学生能否改进；不要重复上一题。"
	if shouldFinish {
		nextAction = "本轮训练结束，不再提问；总结学生目标、已体现能力、主要问题和下一步训练计划。"
	}

	prompt := fmt.Sprintf(
		abilityTrainingTurnPrompt,
		formatTrainingDefinition(s.definition),
		goal,
		lastTask,
		strings.TrimSpace(input),
		strings.Join(issues, "；"),
		nextAction,
	)
	resp, err := s.chatModel.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return nil, fmt.Errorf("%s: continue training: %w", s.Name(), err)
	}

	turn := abilityTrainingTurn{}
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Content)), &turn); err != nil {
		return nil, fmt.Errorf("%s: parse training feedback: %w", s.Name(), err)
	}
	if strings.TrimSpace(turn.AbilityIssue) != "" {
		issues = append(issues, strings.TrimSpace(turn.AbilityIssue))
	}
	state.Data["ability_issues"] = issues
	state.Data["dimension_scores"] = turn.DimensionScores

	var content strings.Builder
	content.WriteString("**训练反馈**\n")
	content.WriteString(strings.TrimSpace(turn.Feedback))
	content.WriteString("\n\n**发现的能力问题**\n")
	content.WriteString(strings.TrimSpace(turn.AbilityIssue))
	content.WriteString("\n\n**成长建议**\n")
	content.WriteString(strings.TrimSpace(turn.GrowthSuggestion))

	if shouldFinish {
		state.Data["phase"] = "done"
		content.WriteString("\n\n**训练总结**\n")
		content.WriteString(strings.TrimSpace(turn.Summary))
		content.WriteString("\n\n**评价维度**\n")
		content.WriteString(formatDimensionScores(s.definition.EvaluationDimensions, turn.DimensionScores))
		return &SkillResponse{Content: content.String(), Done: true, State: state}, nil
	}

	nextQuestion := strings.TrimSpace(turn.NextQuestion)
	if nextQuestion == "" {
		nextQuestion = "请换一个具体情境，说明你会如何应用刚才的成长建议。"
	}
	state.Data["last_task"] = nextQuestion
	content.WriteString("\n\n**下一步训练**\n")
	content.WriteString(nextQuestion)

	return &SkillResponse{
		Content:    content.String(),
		Done:       false,
		NextPrompt: "请继续说明你的思考过程和依据（输入 /quit 退出）",
		State:      state,
	}, nil
}

func (s *AbilityTrainingSkill) fetchRAGContext(ctx context.Context, query, userID string) string {
	if userID == "" {
		userID = "default_user"
	}
	searchQuery := s.definition.Description + " " + query
	var docs []*schema.Document
	if s.milvusStore != nil {
		if results, err := s.milvusStore.RetrieveByUser(ctx, userID, searchQuery); err == nil {
			docs = append(docs, results...)
		}
	}
	if s.bm25Manager != nil {
		if results, err := s.bm25Manager.Retrieve(ctx, userID, searchQuery); err == nil {
			docs = append(docs, results...)
		}
	}
	if len(docs) > 3 {
		docs = docs[:3]
	}

	parts := make([]string, 0, len(docs))
	for _, doc := range docs {
		if text := strings.TrimSpace(doc.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n---\n")
}

func formatTrainingDefinition(definition TrainingSkillDefinition) string {
	return fmt.Sprintf(
		"适用场景：%s\n训练目标：%s\nAgent 行为规则：%s\n评价维度：%s",
		strings.Join(definition.ApplicableScenarios, "；"),
		strings.Join(definition.TrainingGoals, "；"),
		strings.Join(definition.AgentBehaviorRules, "；"),
		strings.Join(definition.EvaluationDimensions, "；"),
	)
}

func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func formatDimensionScores(dimensions []string, scores map[string]float64) string {
	if len(scores) == 0 {
		return "当前证据不足，暂不形成维度分数。"
	}
	seen := make(map[string]bool, len(dimensions))
	lines := make([]string, 0, len(scores))
	for _, dimension := range dimensions {
		seen[dimension] = true
		if score, ok := scores[dimension]; ok {
			lines = append(lines, fmt.Sprintf("- %s：%.0f", dimension, score))
		}
	}
	var extras []string
	for dimension := range scores {
		if !seen[dimension] {
			extras = append(extras, dimension)
		}
	}
	sort.Strings(extras)
	for _, dimension := range extras {
		lines = append(lines, fmt.Sprintf("- %s：%.0f", dimension, scores[dimension]))
	}
	return strings.Join(lines, "\n")
}

// NewLogicalThinkingSkill 创建逻辑思维训练 Skill。
func NewLogicalThinkingSkill(chatModel model.ChatModel, milvusStore *rag.MilvusStore, bm25Manager *rag.BM25Manager) *AbilityTrainingSkill {
	return newAbilityTrainingSkill(TrainingSkillDefinition{
		Name:        "logical-thinking",
		Description: "逻辑思维训练",
		ApplicableScenarios: []string{
			"梳理复杂信息与因果关系", "检查论证链条", "解释解题或判断过程",
		},
		TrainingGoals: []string{
			"区分前提、推理与结论", "形成有顺序的推理链", "发现矛盾、跳步与隐藏假设",
		},
		AgentBehaviorRules: []string{
			"要求学生说出每一步依据", "用反例或条件变化检验推理", "只提示断点，不代替学生完成推导",
		},
		EvaluationDimensions: []string{"结构完整性", "推理有效性", "前后一致性", "依据充分性"},
	}, []string{"逻辑思维", "逻辑训练", "推理训练", "论证分析", "思路混乱"}, chatModel, milvusStore, bm25Manager)
}

// NewCommunicationTrainingSkill 创建沟通表达训练 Skill。
func NewCommunicationTrainingSkill(chatModel model.ChatModel, milvusStore *rag.MilvusStore, bm25Manager *rag.BM25Manager) *AbilityTrainingSkill {
	return newAbilityTrainingSkill(TrainingSkillDefinition{
		Name:        "communication-training",
		Description: "沟通表达训练",
		ApplicableScenarios: []string{
			"向不同对象解释观点", "小组讨论与意见协调", "总结汇报与口头表达",
		},
		TrainingGoals: []string{
			"清晰组织信息", "根据听众调整表达", "准确回应他人观点并推动共识",
		},
		AgentBehaviorRules: []string{
			"明确听众、目的与表达限制", "要求学生重述和澄清", "反馈聚焦表达效果而非语言风格偏好",
		},
		EvaluationDimensions: []string{"表达清晰度", "信息结构", "对象意识", "回应与互动", "语言准确性"},
	}, []string{"沟通训练", "表达训练", "表达能力", "口头表达", "说不清楚", "总结表达"}, chatModel, milvusStore, bm25Manager)
}

// NewProblemSolvingSkill 创建问题解决训练 Skill。
func NewProblemSolvingSkill(chatModel model.ChatModel, milvusStore *rag.MilvusStore, bm25Manager *rag.BM25Manager) *AbilityTrainingSkill {
	return newAbilityTrainingSkill(TrainingSkillDefinition{
		Name:        "problem-solving",
		Description: "问题解决训练",
		ApplicableScenarios: []string{
			"处理开放性任务", "解决学习项目中的障碍", "在限制条件下设计方案",
		},
		TrainingGoals: []string{
			"准确界定问题", "拆解约束并比较方案", "制定行动步骤并验证结果",
		},
		AgentBehaviorRules: []string{
			"先追问目标、条件和已知信息", "引导比较至少两种方案", "要求设置验证标准并复盘结果",
		},
		EvaluationDimensions: []string{"问题界定", "要素分析", "策略选择", "执行规划", "结果验证"},
	}, []string{"问题解决", "解决问题", "解题训练", "方案设计", "遇到困难", "拆解问题"}, chatModel, milvusStore, bm25Manager)
}

// NewCriticalThinkingSkill 创建批判性思维训练 Skill。
func NewCriticalThinkingSkill(chatModel model.ChatModel, milvusStore *rag.MilvusStore, bm25Manager *rag.BM25Manager) *AbilityTrainingSkill {
	return newAbilityTrainingSkill(TrainingSkillDefinition{
		Name:        "critical-thinking",
		Description: "批判性思维训练",
		ApplicableScenarios: []string{
			"判断信息可信度", "比较相互冲突的观点", "评估论据与结论边界",
		},
		TrainingGoals: []string{
			"区分事实、观点与假设", "评价证据质量", "考虑反例和替代解释",
		},
		AgentBehaviorRules: []string{
			"追问信息来源和证据", "要求提出可能的反证", "允许暂缓结论并明确不确定性",
		},
		EvaluationDimensions: []string{"证据意识", "提问质量", "假设识别", "多角度分析", "结论边界"},
	}, []string{"批判性思维", "批判思考", "质疑信息", "判断可信", "是否可信", "论据分析", "辨别观点"}, chatModel, milvusStore, bm25Manager)
}

// NewReflectionTrainingSkill 创建反思训练 Skill。
func NewReflectionTrainingSkill(chatModel model.ChatModel, milvusStore *rag.MilvusStore, bm25Manager *rag.BM25Manager) *AbilityTrainingSkill {
	return newAbilityTrainingSkill(TrainingSkillDefinition{
		Name:        "reflection-training",
		Description: "反思与迁移训练",
		ApplicableScenarios: []string{
			"任务完成后的复盘", "分析重复出现的错误", "把有效策略迁移到新任务",
		},
		TrainingGoals: []string{
			"基于证据重建学习过程", "识别可控原因", "形成可验证的改进计划",
		},
		AgentBehaviorRules: []string{
			"区分结果问题与过程问题", "连续追问原因和策略选择", "把建议落实为下一次可观察行动",
		},
		EvaluationDimensions: []string{"自我觉察", "原因分析", "策略调整", "迁移意识", "行动计划"},
	}, []string{"反思训练", "学习复盘", "复盘学习", "复盘训练", "总结反思", "错误复盘", "训练复盘"}, chatModel, milvusStore, bm25Manager)
}
