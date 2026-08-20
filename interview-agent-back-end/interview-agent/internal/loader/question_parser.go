package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	imodel "interview-agent/internal/model"
)

// ParsedQuestion LLM 解析出的结构化题目
type ParsedQuestion struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Reference  string   `json:"reference"`
	Difficulty string   `json:"difficulty"`
	Type       string   `json:"type"`
	Skills     []string `json:"skills"`
}

// ParseResult 题库解析结果反馈
type ParseResult struct {
	Total     int              `json:"total"`     // LLM 识别出的题目总数
	Success   int              `json:"success"`   // 校验通过并录入的题目数
	Failed    int              `json:"failed"`    // 校验失败的题目数
	Errors    []ParseError     `json:"errors"`    // 每道失败题的具体原因
	Questions []ParsedQuestion `json:"questions"` // 校验通过的题目
}

// ParseError 单道题的校验错误
type ParseError struct {
	Index  int    `json:"index"`  // 题目在原始解析结果中的序号（从 1 开始）
	Reason string `json:"reason"` // 校验失败原因
}

const questionParserPrompt = `你是一名学生能力训练题库解析专家。请从以下文本中提取逻辑思维、沟通表达、问题解决、批判性思维或反思迁移训练题，并转换为结构化 JSON 格式。

## 输入文本
%s

## 输出要求

请输出一个 JSON 数组，每道题包含以下字段：

[
  {
    "content": "完整的学生能力训练问题或学习情境任务",
    "reference": "参考思考路径、关键依据或观察指标",
    "difficulty": "easy 或 medium 或 hard",
    "type": "theory 或 practice 或 scenario",
    "skills": ["教学能力标签1", "教学能力标签2"]
  }
]

## 严格规则（必须遵守）

1. difficulty 只能是以下三个值之一：easy、medium、hard。根据题目考察深度判断。
2. type 只能是以下三个值之一：
   - theory：概念理解、逻辑关系、证据判断与方法原理
   - practice：表达组织、解题过程、学习任务与行动方案
   - scenario：真实学习、协作沟通、信息判断与迁移应用情境
3. skills 必须是非空数组，填写该题训练的学生能力（如 "逻辑思维"、"沟通表达"、"问题解决"、"批判性思维"、"反思迁移"）。
4. content 必须是完整的题目文本，不要截断。
5. reference 必须完整照搬原文中的答案内容，不得改写、缩写或重新组织。如果原文没有答案，填空字符串 ""。
6. 只输出 JSON 数组，不要输出任何其他文字。

请严格按照上述格式输出，不要添加额外字段，不要修改字段名称。`

var (
	validDifficulties = map[string]bool{"easy": true, "medium": true, "hard": true}
	validTypes        = map[string]bool{"theory": true, "practice": true, "scenario": true}
)

// ParseQuestionBank 解析非结构化题库文本为结构化题目
// 超过 maxSegmentLen 的文本会按段落边界分段，逐段调用 LLM 解析后合并结果
func ParseQuestionBank(ctx context.Context, chatModel model.ChatModel, rawText string) (*ParseResult, error) {
	const maxSegmentLen = 25000 // 单段最大字符数，留足 prompt 模板和 LLM 输出空间

	var allRaw []ParsedQuestion

	if len(rawText) <= maxSegmentLen {
		parsed, err := parseSegment(ctx, chatModel, rawText)
		if err != nil {
			return nil, fmt.Errorf("question_parser: %w", err)
		}
		allRaw = parsed
	} else {
		segments := splitByQuestion(rawText, maxSegmentLen)
		log.Printf("[question_parser] 文本过长（%d 字符），拆分为 %d 段分别解析", len(rawText), len(segments))
		for i, seg := range segments {
			log.Printf("[question_parser] 解析第 %d/%d 段（%d 字符）...", i+1, len(segments), len(seg))
			segCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			parsed, err := parseSegment(segCtx, chatModel, seg)
			cancel()
			if err != nil {
				log.Printf("[question_parser] 第 %d 段解析失败: %v，跳过", i+1, err)
				continue
			}
			log.Printf("[question_parser] 第 %d 段解析出 %d 道题", i+1, len(parsed))
			allRaw = append(allRaw, parsed...)
		}
	}

	var allParsed []ParsedQuestion
	var allErrors []ParseError

	for i, q := range allRaw {
		idx := i + 1
		if err := validateQuestion(&q); err != nil {
			allErrors = append(allErrors, ParseError{
				Index:  idx,
				Reason: err.Error(),
			})
			continue
		}
		q.ID = fmt.Sprintf("user_%d_%d", time.Now().Unix(), idx)
		allParsed = append(allParsed, q)
	}

	return &ParseResult{
		Total:     len(allRaw),
		Success:   len(allParsed),
		Failed:    len(allErrors),
		Errors:    allErrors,
		Questions: allParsed,
	}, nil
}

// parseSegment 对单段文本调用 LLM 解析
func parseSegment(ctx context.Context, chatModel model.ChatModel, text string) ([]ParsedQuestion, error) {
	prompt := fmt.Sprintf(questionParserPrompt, text)
	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("question_parser: llm generate: %w", err)
	}

	// 提取 JSON（LLM 可能输出 markdown 代码块包裹的 JSON）
	content := extractJSONArray(resp.Content)

	var questions []ParsedQuestion
	if err := json.Unmarshal([]byte(content), &questions); err != nil {
		// LLM 输出可能被截断，尝试修复：找到最后一个完整的 JSON 对象并补上 ]
		repaired := tryRepairJSONArray(content)
		if repaired != "" {
			if err2 := json.Unmarshal([]byte(repaired), &questions); err2 == nil {
				log.Printf("[question_parser] JSON 被截断，自动修复成功（恢复 %d 道题）", len(questions))
				return questions, nil
			}
		}
		return nil, fmt.Errorf("question_parser: json parse: %w", err)
	}

	return questions, nil
}

// tryRepairJSONArray 尝试修复被截断的 JSON 数组
// 找到最后一个完整的 "}," 或 "}" 并补上 "]"
func tryRepairJSONArray(content string) string {
	// 从后往前找最后一个完整的 JSON 对象结尾 "}"
	for i := len(content) - 1; i >= 0; i-- {
		if content[i] == '}' {
			repaired := strings.TrimRight(content[:i+1], ", \t\n\r") + "]"
			// 确保以 [ 开头
			start := strings.Index(repaired, "[")
			if start >= 0 {
				return repaired[start:]
			}
			break
		}
	}
	return ""
}

// validateQuestion 校验单道题的字段合法性
func validateQuestion(q *ParsedQuestion) error {
	q.Content = strings.TrimSpace(q.Content)
	q.Reference = strings.TrimSpace(q.Reference)
	q.Difficulty = strings.TrimSpace(strings.ToLower(q.Difficulty))
	q.Type = imodel.NormalizeQuestionType(strings.TrimSpace(strings.ToLower(q.Type)))

	if len(q.Content) < 10 {
		return fmt.Errorf("content 长度不足（仅 %d 字符，要求 ≥ 10）", len(q.Content))
	}
	if len(q.Reference) < 10 {
		return fmt.Errorf("reference 长度不足（仅 %d 字符，要求 ≥ 10）", len(q.Reference))
	}
	if !validDifficulties[q.Difficulty] {
		return fmt.Errorf("difficulty 值 %q 不在合法枚举中（仅允许 easy/medium/hard）", q.Difficulty)
	}
	if !validTypes[q.Type] {
		return fmt.Errorf("type 值 %q 不在合法枚举中（仅允许 theory/practice/scenario）", q.Type)
	}
	if len(q.Skills) == 0 {
		return fmt.Errorf("skills 为空数组")
	}
	// 清理 skills 中的空值
	cleaned := make([]string, 0, len(q.Skills))
	for _, s := range q.Skills {
		s = strings.TrimSpace(s)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("skills 全部为空字符串")
	}
	q.Skills = cleaned

	return nil
}

// splitByQuestion 按题目标题边界分段，每段不超过 maxLen 字符
// 题库 MD 的每道题以 "### " 或 "## " 开头，以此为切割点确保不会从题目中间断开
func splitByQuestion(text string, maxLen int) []string {
	lines := strings.Split(text, "\n")

	// 找出所有题目标题的行号作为切割点
	var splitPoints []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			splitPoints = append(splitPoints, i)
		}
	}

	// 没有找到标题，回退到按段落分割
	if len(splitPoints) == 0 {
		return splitByParagraph(text, maxLen)
	}

	// 按标题切割成独立题目块
	var blocks []string
	for i, sp := range splitPoints {
		var end int
		if i+1 < len(splitPoints) {
			end = splitPoints[i+1]
		} else {
			end = len(lines)
		}
		block := strings.Join(lines[sp:end], "\n")
		block = strings.TrimSpace(block)
		if block != "" {
			blocks = append(blocks, block)
		}
	}
	// 标题前的内容（如文件开头的说明文字）忽略
	if len(splitPoints) > 0 && splitPoints[0] > 0 {
		header := strings.TrimSpace(strings.Join(lines[:splitPoints[0]], "\n"))
		if header != "" {
			// 开头说明文字不含题目，跳过
			log.Printf("[question_parser] 跳过标题前的说明文字（%d 字符）", len(header))
		}
	}

	// 将题目块合并为不超过 maxLen 的段
	var segments []string
	var current strings.Builder

	for _, b := range blocks {
		if current.Len() > 0 && current.Len()+len(b)+2 > maxLen {
			segments = append(segments, current.String())
			current.Reset()
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(b)
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}

	if len(segments) == 0 && text != "" {
		segments = []string{text}
	}

	return segments
}

// splitByParagraph 按段落边界分段（回退方案），每段不超过 maxLen 字符
func splitByParagraph(text string, maxLen int) []string {
	paragraphs := strings.Split(text, "\n\n")

	var segments []string
	var current strings.Builder

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		if current.Len() > 0 && current.Len()+len(p)+2 > maxLen {
			segments = append(segments, current.String())
			current.Reset()
		}

		if current.Len() == 0 && len(p) > maxLen {
			segments = append(segments, p)
			continue
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}

	if len(segments) == 0 && text != "" {
		segments = []string{text}
	}

	return segments
}

// extractJSONArray 从 LLM 输出中提取 JSON 数组
func extractJSONArray(text string) string {
	trimmed := strings.TrimSpace(text)

	// 如果文本直接以 [ 开头，说明 LLM 直接输出了 JSON 数组（无 markdown 包裹）
	// 此时不能用 ``` 做提取，因为 JSON 值内部可能包含代码块标记
	if strings.HasPrefix(trimmed, "[") {
		end := strings.LastIndex(trimmed, "]")
		if end > 0 {
			return trimmed[:end+1]
		}
		return trimmed
	}

	// LLM 用 markdown 代码块包裹了 JSON，提取代码块内容
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	// 兜底：直接查找 JSON 数组的起止
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start >= 0 && end > start {
		return text[start : end+1]
	}

	return text
}
