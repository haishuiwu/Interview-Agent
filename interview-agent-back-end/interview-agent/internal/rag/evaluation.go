/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// EvalResult RAG 评估结果
type EvalResult struct {
	Faithfulness float64 `json:"faithfulness"` // 忠实度（0-1）：回答是否基于检索文档
	Relevance    float64 `json:"relevance"`    // 相关性（0-1）：检索文档是否与问题相关
	Completeness float64 `json:"completeness"` // 完整性（0-1）：回答是否覆盖了所有要点
	Overall      float64 `json:"overall"`      // 综合得分
}

const evalPrompt = `你是一个 RAG（检索增强生成）质量评估专家。请评估以下 RAG 系统的输出质量。

## 用户问题
%s

## 检索到的参考文档
%s

## 系统生成的回答
%s

请从以下三个维度评分（0-1 之间的小数），并输出纯 JSON：

{
  "faithfulness": 0.0,
  "relevance": 0.0,
  "completeness": 0.0,
  "reasoning": "评估理由"
}

评分标准：
- faithfulness（忠实度）：回答是否完全基于检索到的文档？是否有臆造内容？
- relevance（相关性）：检索到的文档是否与用户问题相关？
- completeness（完整性）：回答是否覆盖了问题的所有方面？是否有遗漏？`

// Evaluator RAG 质量评估器
type Evaluator struct {
	chatModel model.ChatModel
}

// NewEvaluator 创建 RAG 评估器
func NewEvaluator(chatModel model.ChatModel) *Evaluator {
	return &Evaluator{chatModel: chatModel}
}

// Evaluate 评估 RAG 输出质量
func (e *Evaluator) Evaluate(ctx context.Context, question string, docs []*schema.Document, answer string) (*EvalResult, error) {
	// 格式化检索文档
	var docsText string
	for i, doc := range docs {
		docsText += fmt.Sprintf("[文档%d] %s\n", i+1, doc.Content)
	}

	prompt := fmt.Sprintf(evalPrompt, question, docsText, answer)
	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := e.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("rag_evaluator: generate: %w", err)
	}

	var raw struct {
		Faithfulness float64 `json:"faithfulness"`
		Relevance    float64 `json:"relevance"`
		Completeness float64 `json:"completeness"`
		Reasoning    string  `json:"reasoning"`
	}
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("rag_evaluator: parse response: %w", err)
	}

	return &EvalResult{
		Faithfulness: raw.Faithfulness,
		Relevance:    raw.Relevance,
		Completeness: raw.Completeness,
		Overall:      (raw.Faithfulness + raw.Relevance + raw.Completeness) / 3,
	}, nil
}

// QuestionBankEvalResult 题库诊断评估结果
type QuestionBankEvalResult struct {
	Precision     float64           `json:"precision"`      // 精确率：检索到的题中有多少是真正相关的
	Recall        float64           `json:"recall"`         // 召回率（近似）：JD 技能方向被题库覆盖的比例
	Relevance     float64           `json:"relevance"`      // 语义相关性：检索结果与 JD 的语义匹配程度
	Completeness  float64           `json:"completeness"`   // 技能覆盖度：同 recall，保留兼容
	Overall       float64           `json:"overall"`        // 综合得分
	Summary       string            `json:"summary"`        // 诊断摘要
	SkillCoverage []SkillCoverage   `json:"skill_coverage"` // 各技能方向覆盖详情
	QuestionEvals []QuestionEval    `json:"question_evals"` // 每道检索题的逐题评估
}

// SkillCoverage 单个技能方向的覆盖情况
type SkillCoverage struct {
	Skill    string  `json:"skill"`    // 技能名称
	Covered  bool    `json:"covered"`  // 是否有题目覆盖
	Quality  string  `json:"quality"`  // 覆盖质量：充足/偏少/缺失
}

// QuestionEval 单道检索题目的评估
type QuestionEval struct {
	Index    int    `json:"index"`    // 题目序号
	Relevant bool   `json:"relevant"` // 是否与 JD 相关
	Reason   string `json:"reason"`   // 判断理由
}

const questionBankEvalPrompt = `你是一个 RAG 检索质量评估专家。请评估以下从题库中检索到的题目对目标岗位 JD 的匹配质量。

## 目标岗位 JD
%s

## 从题库中检索到的题目（共 %d 道）
%s

请严格按以下步骤评估，并输出纯 JSON：

第一步：逐题判断相关性——每道检索到的题目是否与该 JD 的技能要求相关？
第二步：提取 JD 中的核心技能方向，逐一检查题库是否覆盖
第三步：计算指标

{
  "precision": 0.0,
  "recall": 0.0,
  "relevance": 0.0,
  "summary": "给面试者的诊断建议（2-3句话）",
  "skill_coverage": [
    {"skill": "技能名称", "covered": true, "quality": "充足/偏少/缺失"}
  ],
  "question_evals": [
    {"index": 1, "relevant": true, "reason": "简要理由"}
  ]
}

指标计算方式：
- precision（精确率）= 检索到的题目中相关题数 / 检索到的总题数。衡量检索的准确程度。
- recall（召回率）= 题库覆盖的 JD 技能方向数 / JD 总技能方向数。衡量题库对 JD 的覆盖程度。
- relevance（语义相关性，0-1）：整体来看，检索结果与 JD 岗位需求的语义匹配程度。
- question_evals：必须对每道题逐一评估是否与 JD 相关。
- skill_coverage：必须列出 JD 中的每个核心技能方向。`

// EvaluateQuestionBank 评估题库与 JD 的匹配质量（在线题库诊断）
func (e *Evaluator) EvaluateQuestionBank(ctx context.Context, jdSkills string, docs []*schema.Document) (*QuestionBankEvalResult, error) {
	var docsText string
	for i, doc := range docs {
		docsText += fmt.Sprintf("[题目%d] %s\n", i+1, doc.Content)
	}

	prompt := fmt.Sprintf(questionBankEvalPrompt, jdSkills, len(docs), docsText)
	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := e.chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("rag_evaluator: evaluate question bank: %w", err)
	}

	var result QuestionBankEvalResult
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("rag_evaluator: parse question bank eval: %w", err)
	}

	result.Completeness = result.Recall // 保持兼容
	result.Overall = (result.Precision + result.Recall + result.Relevance) / 3
	return &result, nil
}
