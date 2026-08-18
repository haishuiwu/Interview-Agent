/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package loader

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/mcp"
)

// WebScraper 持有 MCP 网页抓取器的引用，供全局使用
var webScraper *mcp.WebScraper

// InitWebScraper 初始化 MCP 网页抓取器（程序启动时调用）
func InitWebScraper() error {
	ws, err := mcp.NewWebScraper()
	if err != nil {
		return err
	}
	webScraper = ws
	return nil
}

// CloseWebScraper 关闭 MCP 网页抓取器
func CloseWebScraper() {
	if webScraper != nil {
		webScraper.Close()
	}
}

// FetchURL 通过 MCP Playwright 抓取网页内容（支持 JS 渲染的 SPA 页面）
func FetchURL(ctx context.Context, url string) (string, error) {
	if webScraper == nil {
		return "", fmt.Errorf("loader: MCP 网页抓取器未初始化，请先调用 InitWebScraper()")
	}

	return webScraper.ScrapeURL(ctx, url)
}

// ExtractJDFromURL 通过 MCP 抓取网页 + 用 LLM 提取 JD 正文
// 招聘页面通常夹杂导航和广告，用 LLM 从抓取内容中提取核心 JD
func ExtractJDFromURL(ctx context.Context, url string, chatModel model.ChatModel) (string, error) {
	rawText, err := FetchURL(ctx, url)
	if err != nil {
		return "", err
	}

	// 截断过长的网页内容（避免超出 token 限制）
	if len(rawText) > 10000 {
		rawText = rawText[:10000]
	}

	prompt := `以下是从网页抓取的内容，其中可能包含教师岗位描述、教师资格面试大纲或教学能力考核标准，也包含无关的导航和广告。
请只提取与教师训练目标相关的正文，包括：岗位或考核名称、学段学科、教学职责、能力要求、考核环节与评价重点。
只输出可作为训练标准的正文，不要输出其他内容。

原始页面内容：
` + rawText

	messages := []*schema.Message{
		schema.UserMessage(prompt),
	}

	resp, err := chatModel.Generate(ctx, messages)
	if err != nil {
		// LLM 提取失败，退化为返回原始文本
		return rawText, nil
	}

	// 检测 LLM 是否真的提取到了有效 JD（而非返回"无法提取"等错误描述）
	respLower := strings.ToLower(resp.Content)
	invalidMarkers := []string{"无法提取", "无法识别", "未包含", "不包含", "没有找到", "错误页", "err_"}
	for _, marker := range invalidMarkers {
		if strings.Contains(respLower, marker) {
			return "", fmt.Errorf("loader: 该链接未包含有效的职位描述，请直接粘贴 JD 文本或上传文件")
		}
	}

	return resp.Content, nil
}

// IsURL 判断输入是否是 URL
func IsURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// IsFilePath 判断输入是否是文件路径
func IsFilePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// 以 / 、 ./ 、 ~/ 开头，或包含文件扩展名
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "~/") {
		return true
	}
	ext := strings.ToLower(s)
	return strings.HasSuffix(ext, ".pdf") || strings.HasSuffix(ext, ".docx") ||
		strings.HasSuffix(ext, ".txt") || strings.HasSuffix(ext, ".md")
}
