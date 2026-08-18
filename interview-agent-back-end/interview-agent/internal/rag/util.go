/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

// extractJSON 从可能包含 markdown 代码块的文本中提取 JSON
func extractJSON(text string) string {
	start := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '{' || text[i] == '[' {
			start = i
			break
		}
	}
	if start == -1 {
		return text
	}

	// 找到匹配的结束字符
	endChar := byte('}')
	if text[start] == '[' {
		endChar = ']'
	}

	end := -1
	for i := len(text) - 1; i >= start; i-- {
		if text[i] == endChar {
			end = i + 1
			break
		}
	}
	if end == -1 {
		return text
	}

	return text[start:end]
}
