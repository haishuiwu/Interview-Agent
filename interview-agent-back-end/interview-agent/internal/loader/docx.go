/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package loader

import (
	"fmt"
	"strings"

	"github.com/nguyenthenguyen/docx"
)

// loadDOCX 解析 DOCX 文件，提取纯文本
func loadDOCX(path string) (string, error) {
	r, err := docx.ReadDocxFile(path)
	if err != nil {
		return "", fmt.Errorf("loader: 打开 DOCX 失败: %w", err)
	}
	defer r.Close()

	doc := r.Editable()
	content := doc.GetContent()

	// docx 库返回的内容可能含 XML 标签残留，做基本清理
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("loader: DOCX 内容为空: %s", path)
	}

	return content, nil
}
