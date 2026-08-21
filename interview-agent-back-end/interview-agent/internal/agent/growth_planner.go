/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/mcp"
	imodel "interview-agent/internal/model"
)

// growthPlannerInstruction ReAct 系统指令：按学习诊断生成能力提升路径。
const growthPlannerInstruction = `你是一名学生成长规划师，要根据学生的能力训练反馈制定个性化提升计划。

资源优先级依次为：权威课程或考试标准、教材与参考书、优质课程、经典书籍和可执行的刻意练习。
只有当待提升能力明确涉及编程、数据分析、数字工具或技术实践时，才可以调用 search_github_repos 搜索真实开源工具；
不得为了展示工具调用而推荐与训练目标无关的技术项目。工具不可用或没有合适结果时，不编造名称和链接。

规划原则：优先解决高优先级待提升能力；每个学习项包含可观察的目标、刻意练习、反思与复测方式；时间估算合理；不对学生作人格或职业定性。

最终只输出纯 JSON（不要输出工具调用过程、思考说明或多余文字），格式：
{
  "weak_areas": [
    {"topic": "薄弱领域名称", "score": 50.0, "priority": "high/medium/low"}
  ],
  "study_plan": [
    {"topic": "训练主题", "objective": "可观察的提升目标", "actions": ["学习或备课", "模拟试讲或情境练习", "复盘与复测"], "time_estimate": "预估时间"}
  ],
  "resources": [
    {"title": "资源标题", "type": "standard/course/video/book/repo", "url": "已确认的链接（无法确认则留空）", "desc": "与薄弱点的对应关系"}
  ]
}`

// GrowthPlanner 成长规划 Agent —— 保留原有 Eino ReAct 与 MCP 工具调用机制。
type GrowthPlanner struct {
	chatModel      model.ChatModel
	githubSearcher *mcp.GitHubSearcher // 可选，nil 时退化为纯 LLM 生成
}

// NewGrowthPlanner 创建成长规划 Agent。
func NewGrowthPlanner(chatModel model.ChatModel) *GrowthPlanner {
	return &GrowthPlanner{chatModel: chatModel}
}

// SetGitHubSearcher 设置 GitHub MCP 搜索器
func (p *GrowthPlanner) SetGitHubSearcher(searcher *mcp.GitHubSearcher) {
	p.githubSearcher = searcher
}

// Plan 根据训练反馈生成成长计划。
func (p *GrowthPlanner) Plan(ctx context.Context, report *imodel.EvaluationReport) (*imodel.ReviewPlan, error) {
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	userMsg := fmt.Sprintf("请根据以下学生能力训练反馈生成提升计划：\n\n%s", string(reportJSON))

	// 优先用 ReAct（带 GitHub 工具，由模型自主调用）；失败或无工具时降级为单轮生成
	content, err := p.generateWithReactAgent(ctx, userMsg)
	if err != nil || strings.TrimSpace(content) == "" {
		if err != nil {
			log.Printf("[GrowthPlanner] ReAct 执行失败，降级为单轮生成: %v", err)
		}
		messages := []*schema.Message{
			schema.SystemMessage(growthPlannerInstruction),
			schema.UserMessage(userMsg),
		}
		resp, gErr := p.chatModel.Generate(ctx, messages)
		if gErr != nil {
			return nil, fmt.Errorf("growth_planner: generate: %w", gErr)
		}
		content = resp.Content
	}

	result := &imodel.ReviewPlan{}
	jsonStr := extractJSON(content)
	if err := json.Unmarshal([]byte(jsonStr), result); err != nil {
		return nil, fmt.Errorf("growth_planner: parse response: %w\nraw: %s", err, content)
	}

	result.SessionID = report.SessionID
	result.CreatedAt = time.Now()

	return result, nil
}

// generateWithReactAgent 用 Eino ReAct（GitHub 工具由模型自主调用）生成复习计划文本。无工具时返回空串以走降级。
func (p *GrowthPlanner) generateWithReactAgent(ctx context.Context, userMsg string) (string, error) {
	if p.githubSearcher == nil {
		return "", nil // 没有 GitHub 工具，直接走降级
	}

	ghTool, err := utils.InferTool(
		"search_github_repos",
		"仅在提升编程、数据分析、数字工具或技术实践能力时，根据英文关键词搜索 GitHub 开源工具，返回名称、star 数、链接与简介。",
		p.searchGitHubRepos,
	)
	if err != nil {
		return "", fmt.Errorf("build github tool: %w", err)
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		Model:       p.chatModel,
		ToolsConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{ghTool}},
		MessageModifier: func(_ context.Context, input []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage(growthPlannerInstruction)}, input...)
		},
	})
	if err != nil {
		return "", fmt.Errorf("new react agent: %w", err)
	}

	msg, err := agent.Generate(ctx, []*schema.Message{schema.UserMessage(userMsg)})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// githubSearchReq ReAct 调用 GitHub 工具的入参
type githubSearchReq struct {
	Query string `json:"query" jsonschema:"description=编程、数据分析、数字工具或技术实践关键词，用英文"`
}

// searchGitHubRepos 工具实现：按关键词搜索 GitHub 仓库，返回格式化文本
func (p *GrowthPlanner) searchGitHubRepos(ctx context.Context, req githubSearchReq) (string, error) {
	repos, err := p.githubSearcher.SearchRepos(ctx, req.Query+" stars:>100", 5)
	if err != nil || len(repos) == 0 {
		return "未找到相关开源项目。", nil
	}
	var sb strings.Builder
	for i, r := range repos {
		sb.WriteString(fmt.Sprintf("%d. **%s** (%d stars)\n   链接：%s\n   简介：%s\n\n",
			i+1, r.Name, r.Stars, r.URL, r.Desc))
	}
	return sb.String(), nil
}

// FormatReviewPlan 将复习计划格式化为 Markdown
func FormatReviewPlan(plan *imodel.ReviewPlan) string {
	md := "# 学生能力提升计划\n\n"

	md += "## 薄弱领域\n\n"
	md += "| 领域 | 得分 | 优先级 |\n|------|------|--------|\n"
	for _, area := range plan.WeakAreas {
		md += fmt.Sprintf("| %s | %.1f | %s |\n", area.Topic, area.Score, area.Priority)
	}

	md += "\n## 训练计划\n\n"
	for i, item := range plan.StudyPlan {
		md += fmt.Sprintf("### %d. %s\n\n", i+1, item.Topic)
		md += fmt.Sprintf("**目标**：%s\n\n", item.Objective)
		md += fmt.Sprintf("**预估时间**：%s\n\n", item.TimeEstimate)
		md += "**具体行动**：\n"
		for _, action := range item.Actions {
			md += fmt.Sprintf("- %s\n", action)
		}
		md += "\n"
	}

	if len(plan.Resources) > 0 {
		md += "## 推荐资源\n\n"
		for _, res := range plan.Resources {
			md += fmt.Sprintf("- **[%s](%s)**（%s）：%s\n", res.Title, res.URL, res.Type, res.Desc)
		}
	}

	return md
}
