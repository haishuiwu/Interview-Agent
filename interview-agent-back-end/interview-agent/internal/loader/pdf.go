/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package loader

import (
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// loadPDF 解析 PDF 文件，提取纯文本
func loadPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("loader: 打开 PDF 失败: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	totalPage := r.NumPage()
	if totalPage == 0 {
		return "", fmt.Errorf("loader: PDF 页数为 0: %s", path)
	}

	for i := 1; i <= totalPage; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n")
	}

	content := strings.TrimSpace(sb.String())
	if content == "" {
		return "", fmt.Errorf("loader: PDF 未提取到文本（可能是扫描件）: %s", path)
	}
	return content, nil
}
