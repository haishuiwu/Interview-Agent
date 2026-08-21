/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

// Phase 1 prompt：根据能力标准 + 学生画像规划训练方向。
const directionPlannerPrompt = `你是一名学生能力训练规划师。请根据学习目标、能力标准和学生画像的诊断结果，规划本次能力训练的题目方向。

你的任务是：为每道题确定一个考察方向/考点，而不是出具体的题目。

规划原则：
1. 【数量硬性要求，必须严格遵守，每个类型都要按难度分档铺满】：
   - theory 类：每个难度档各 5 个 —— easy 5 个、medium 5 个、hard 5 个，共 15 个
   - practice 类：每个难度档各 4 个 —— easy 4 个、medium 4 个、hard 4 个，共 12 个
   - scenario 类：medium 2 个、hard 2 个，共 4 个
   - 说明：以上是训练候选题池，训练时会按学生实时表现自适应抽取，不要求全部作答；
     因此每档必须铺满，保证同一难度有足够候选可供持续抽取
2. 题型包含三类，以能力标准为边界、以学生画像和薄弱点为个性化依据：
   - theory：概念、原理、规则和知识联系等基础认知
   - practice：围绕学生画像中的真实学习、练习、项目或作品经历进行复盘和应用
   - scenario：综合运用知识分析情境、制定方案并解释依据
3. 每个方向给出一个用于题库检索的关键词（search_query）
4. practice 类必须基于学生画像中的真实信息，context 字段填写相关内容摘要；画像未提供时，改为通用应用任务，不得虚构经历
5. 每个方向的 difficulty 必须标注准确，且严格符合第 1 条按难度分档的数量配额（同一 type 下 easy/medium/hard 的方向数量必须达标）
6. 【严禁幻觉】不得杜撰学习经历、实践成果、作品数据或具体事实
7. 题目服务于能力诊断和训练，不对学生作人格或职业定性

请按以下 JSON 格式输出（不要输出其他内容）：

{
  "directions": [
    {
      "topic": "训练方向描述（如：依据学情设计分层提问）",
      "type": "theory/practice/scenario",
      "difficulty": "easy/medium/hard",
      "search_query": "题库检索关键词",
      "skills": ["训练和诊断的能力点"],
      "context": "学生画像中的相关上下文（practice 类有真实经历时填写）"
    }
  ]
}`

// Phase 2 prompt：根据方向 + 题库匹配结果生成最终题目
const questionAssemblerPrompt = `你是一名学生能力训练教练。请根据训练方向和题库匹配结果，生成最终的能力训练题目。

规则：
1. 【数量严格对应，最重要的规则】每个出题方向必须对应生成恰好一道题目，不得合并、删减或跳过任何方向。输入 N 个方向就必须输出 N 道题
2. 如果提供了题库匹配的原题，直接使用原题（content 完全照搬不得改编），source 填题目 ID
3. 如果没有匹配到题库原题，由你根据出题方向自行出题，source 填 "llm"
4. 【LLM 出题基于画像】自行出题时，优先结合学生真实的知识基础、学习经历、项目与作品
5. 【严禁幻觉】practice 类题目不得杜撰学生画像中不存在的经历；scenario 类可以给出明确的假设情境
6. 题目 content 必须简洁、可作答，并明确需要学生说明的判断与依据
7. 每道题准备 1-2 个苏格拉底式追问，用于引导学生补足依据、步骤或反思
8. 【难度沿用】每道题的 difficulty 必须与其对应出题方向给定的 difficulty 完全一致，不得更改，以保持整体难度分布的梯度

请按以下 JSON 格式输出（不要输出其他内容）：

{
  "total_questions": 10,
  "distribution": {
    "theory": 0,
    "practice": 0,
    "scenario": 0
  },
  "questions": [
    {
      "id": "q1",
      "content": "题目内容",
      "type": "theory/practice/scenario",
      "difficulty": "easy/medium/hard",
      "skills": ["诊断的能力点"],
      "follow_ups": ["追问1", "追问2"],
      "reference": "参考作答要点与可接受的理由",
      "source": "题库原题ID 或 llm"
    }
  ]
}`

// QuestionPlanner 出题规划 Agent
type QuestionPlanner struct {
	chatModel model.ChatModel
}

// NewQuestionPlanner 创建出题规划 Agent
func NewQuestionPlanner(chatModel model.ChatModel) *QuestionPlanner {
	return &QuestionPlanner{chatModel: chatModel}
}

// PlanDirections Phase 1：根据能力标准与学习诊断规划训练方向。
func (p *QuestionPlanner) PlanDirections(ctx context.Context, standard *imodel.AbilityStandard, diagnosis *imodel.LearningDiagnosis, weakPoints string) (*imodel.QuestionDirectionPlan, error) {
	standardSummary := formatAbilityStandardForDiagnosis(standard)
	diagnosisSummary := formatLearningDiagnosisForTraining(diagnosis)

	userMsg := fmt.Sprintf("## 学习目标与能力标准\n\n%s\n\n## 学习诊断\n\n%s", standardSummary, diagnosisSummary)

	if weakPoints != "" {
		userMsg += fmt.Sprintf("\n\n## 学生历史薄弱点（请针对性加强训练）\n\n%s", weakPoints)
	}

	messages := []*schema.Message{
		schema.SystemMessage(directionPlannerPrompt),
		schema.UserMessage(userMsg),
	}

	resp, err := p.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("question_planner: plan directions: %w", err)
	}

	result := &imodel.QuestionDirectionPlan{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("question_planner: parse directions: %w\nraw: %s", err, resp.Content)
	}
	for i := range result.Directions {
		result.Directions[i].Type = imodel.NormalizeQuestionType(result.Directions[i].Type)
	}

	return result, nil
}

// AssembleQuestions Phase 2：根据方向 + 题库匹配结果生成最终题目。
// directionDocs: 每个方向索引对应的题库匹配文档（可能为空）
func (p *QuestionPlanner) AssembleQuestions(ctx context.Context, standard *imodel.AbilityStandard, diagnosis *imodel.LearningDiagnosis, directions *imodel.QuestionDirectionPlan, directionDocs []string) (*imodel.QuestionPlan, error) {
	standardSummary := formatAbilityStandardForDiagnosis(standard)
	diagnosisSummary := formatLearningDiagnosisForTraining(diagnosis)

	// 构建每个方向 + 对应的题库匹配情况
	var directionsText string
	for i, d := range directions.Directions {
		directionsText += fmt.Sprintf("### 方向 %d: %s\n", i+1, d.Topic)
		directionsText += fmt.Sprintf("- 类型: %s, 难度: %s, 技能: %v\n", d.Type, d.Difficulty, d.Skills)
		if d.Context != "" {
			directionsText += fmt.Sprintf("- 学生画像上下文: %s\n", d.Context)
		}
		if i < len(directionDocs) && directionDocs[i] != "" {
			directionsText += fmt.Sprintf("- 题库匹配原题:\n%s\n", directionDocs[i])
		} else {
			directionsText += "- 题库匹配: 无匹配，请 LLM 自行出题\n"
		}
		directionsText += "\n"
	}

	userMsg := fmt.Sprintf("## 学习目标与能力标准\n\n%s\n\n## 学习诊断\n\n%s\n\n## 训练方向与题库匹配\n\n%s",
		standardSummary, diagnosisSummary, directionsText)

	messages := []*schema.Message{
		schema.SystemMessage(questionAssemblerPrompt),
		schema.UserMessage(userMsg),
	}

	resp, err := p.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("question_planner: assemble questions: %w", err)
	}

	result := &imodel.QuestionPlan{}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), result); err != nil {
		return nil, fmt.Errorf("question_planner: parse questions: %w\nraw: %s", err, resp.Content)
	}
	result.Distribution.Theory = 0
	result.Distribution.Practice = 0
	result.Distribution.Scenario = 0
	for i := range result.Questions {
		result.Questions[i].Type = imodel.NormalizeQuestionType(result.Questions[i].Type)
		switch result.Questions[i].Type {
		case imodel.QuestionTypeTheory:
			result.Distribution.Theory++
		case imodel.QuestionTypePractice:
			result.Distribution.Practice++
		case imodel.QuestionTypeScenario:
			result.Distribution.Scenario++
		}
	}

	return result, nil
}

// AdjustDifficulty 根据训练状态动态调整后续题目难度。
func (p *QuestionPlanner) AdjustDifficulty(state *imodel.TrainingState) imodel.DifficultyLevel {
	// 连续答对 2 题以上 → 提高难度
	if state.ConsecutiveRight >= 2 {
		switch state.CurrentDifficulty {
		case imodel.DifficultyEasy:
			return imodel.DifficultyMedium
		case imodel.DifficultyMedium:
			return imodel.DifficultyHard
		default:
			return imodel.DifficultyHard
		}
	}

	// 连续答错 2 题以上 → 降低难度
	if state.ConsecutiveWrong >= 2 {
		switch state.CurrentDifficulty {
		case imodel.DifficultyHard:
			return imodel.DifficultyMedium
		case imodel.DifficultyMedium:
			return imodel.DifficultyEasy
		default:
			return imodel.DifficultyEasy
		}
	}

	// 保持当前难度
	return state.CurrentDifficulty
}

func formatLearningDiagnosisForTraining(diagnosis *imodel.LearningDiagnosis) string {
	data, _ := json.MarshalIndent(diagnosis, "", "  ")
	return string(data)
}
