/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func testDocs(ids ...string) []*schema.Document {
	docs := make([]*schema.Document, len(ids))
	for i, id := range ids {
		docs[i] = &schema.Document{ID: id, Content: "content-" + id}
	}
	return docs
}

func docIDs(docs []*schema.Document) []string {
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mockRerankServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func newTestCrossEncoder(endpoint string, topK int) *CrossEncoderReranker {
	r := NewCrossEncoderReranker("test-key", "gte-rerank-v2", topK)
	r.endpoint = endpoint
	return r
}

// 按模型返回的 index 顺序把候选映射回文档
func TestCrossEncoderReranker_ReordersByModelOutput(t *testing.T) {
	// 输入 a,b,c（index 0,1,2）；模型重排为 index 2,0,1 → c,a,b
	srv := mockRerankServer(200, `{"output":{"results":[{"index":2},{"index":0},{"index":1}]}}`)
	defer srv.Close()
	r := newTestCrossEncoder(srv.URL, 10)
	out, err := r.Rerank(context.Background(), "q", testDocs("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if got := docIDs(out); !equalIDs(got, []string{"c", "a", "b"}) {
		t.Fatalf("期望 [c a b]，得到 %v", got)
	}
}

// 模型漏返的文档按原顺序补末尾，不缩小召回集合
func TestCrossEncoderReranker_AppendsMissing(t *testing.T) {
	srv := mockRerankServer(200, `{"output":{"results":[{"index":2},{"index":0}]}}`) // 漏了 index 1
	defer srv.Close()
	r := newTestCrossEncoder(srv.URL, 10)
	out, _ := r.Rerank(context.Background(), "q", testDocs("a", "b", "c"))
	if len(out) != 3 {
		t.Fatalf("不应丢文档，得到 %d 条", len(out))
	}
	if got := docIDs(out); !equalIDs(got, []string{"c", "a", "b"}) {
		t.Fatalf("期望补漏后 [c a b]，得到 %v", got)
	}
}

// 结果截断到 topK
func TestCrossEncoderReranker_TruncateTopK(t *testing.T) {
	srv := mockRerankServer(200, `{"output":{"results":[{"index":2},{"index":0},{"index":1}]}}`)
	defer srv.Close()
	r := newTestCrossEncoder(srv.URL, 2)
	out, _ := r.Rerank(context.Background(), "q", testDocs("a", "b", "c"))
	if got := docIDs(out); !equalIDs(got, []string{"c", "a"}) {
		t.Fatalf("期望 [c a]，得到 %v", got)
	}
}

// 403（如 gte-rerank 无权限）等错误降级为原始顺序
func TestCrossEncoderReranker_DegradeOn403(t *testing.T) {
	srv := mockRerankServer(403, `{"code":"AccessDenied"}`)
	defer srv.Close()
	r := newTestCrossEncoder(srv.URL, 10)
	out, err := r.Rerank(context.Background(), "q", testDocs("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if got := docIDs(out); !equalIDs(got, []string{"a", "b", "c"}) {
		t.Fatalf("403 应降级为原始顺序，得到 %v", got)
	}
}

// none 策略：原序截断
func TestNoneReranker_Truncate(t *testing.T) {
	r := NewNoneReranker(2)
	out, _ := r.Rerank(context.Background(), "q", testDocs("a", "b", "c"))
	if got := docIDs(out); !equalIDs(got, []string{"a", "b"}) {
		t.Fatalf("期望 [a b]，得到 %v", got)
	}
}
