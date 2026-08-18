package speech

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "source marker and markdown",
			input: "## **请介绍** Go 的 `GMP` 调度模型。\n\n`[来源: LLM 出题]`",
			want:  "请介绍 Go 的 GMP 调度模型。",
		},
		{
			name:  "link quote and whitespace",
			input: "> 请阅读 [Go 官方文档](https://go.dev/doc/) 后回答：\n\n  goroutine   是什么？",
			want:  "请阅读 Go 官方文档 后回答： goroutine 是什么？",
		},
		{
			name:  "fenced code",
			input: "分析下面代码：\n```go\nfunc main() { panic(\"boom\") }\n```\n它有什么问题？",
			want:  "分析下面代码： 请查看屏幕中的代码 它有什么问题？",
		},
		{
			name:  "mixed language and punctuation",
			input: "请解释 Go 1.26 中的 scheduler、P/M/G，以及 99.9% latency。",
			want:  "请解释 Go 1.26 中的 scheduler、P/M/G，以及 99.9% latency。",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeText(tc.input, 500)
			if err != nil {
				t.Fatalf("NormalizeText: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeTextRejectsEmptyAndTooLong(t *testing.T) {
	if _, err := NormalizeText("  ` [来源: LLM 出题] `  ", 500); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty normalized text error = %v, want ErrInvalidRequest", err)
	}
	if _, err := NormalizeText(strings.Repeat("好", 6), 5); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("long normalized text error = %v, want ErrTextTooLong", err)
	}
}
