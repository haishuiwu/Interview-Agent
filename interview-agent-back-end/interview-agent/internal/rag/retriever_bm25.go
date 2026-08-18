/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// BM25Retriever 基于 BM25 算法的关键词检索器
type BM25Retriever struct {
	documents []*schema.Document
	index     map[string][]docTF // 倒排索引：词 -> [(docIndex, 词频)]
	docLen    []int              // 每篇文档的词数
	avgDL     float64            // 平均文档长度
	topK      int
	k1        float64 // BM25 参数，控制词频饱和度
	b         float64 // BM25 参数，控制文档长度归一化
}

type docTF struct {
	docIdx int
	tf     int
}

// NewBM25Retriever 创建 BM25 检索器
func NewBM25Retriever(topK int) *BM25Retriever {
	if topK <= 0 {
		topK = 10
	}
	return &BM25Retriever{
		index: make(map[string][]docTF),
		topK:  topK,
		k1:    1.5,
		b:     0.75,
	}
}

// GetDocuments 获取当前索引中的所有文档
func (r *BM25Retriever) GetDocuments() []*schema.Document {
	return r.documents
}

// IndexDocuments 构建 BM25 倒排索引
func (r *BM25Retriever) IndexDocuments(docs []*schema.Document) {
	r.documents = docs
	r.index = make(map[string][]docTF)
	r.docLen = make([]int, len(docs))

	totalLen := 0
	for i, doc := range docs {
		tokens := tokenize(doc.Content)
		r.docLen[i] = len(tokens)
		totalLen += len(tokens)

		// 统计每个词在文档中的词频
		tfMap := make(map[string]int)
		for _, t := range tokens {
			tfMap[t]++
		}

		for term, tf := range tfMap {
			r.index[term] = append(r.index[term], docTF{docIdx: i, tf: tf})
		}
	}

	if len(docs) > 0 {
		r.avgDL = float64(totalLen) / float64(len(docs))
	}
}

// Retrieve 实现 retriever.Retriever 接口
func (r *BM25Retriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	if len(r.documents) == 0 {
		return nil, nil
	}

	queryTokens := tokenize(query)
	n := len(r.documents)
	scores := make([]float64, n)

	for _, term := range queryTokens {
		postings, ok := r.index[term]
		if !ok {
			continue
		}

		// IDF = log((N - df + 0.5) / (df + 0.5) + 1)
		df := float64(len(postings))
		idf := math.Log((float64(n)-df+0.5)/(df+0.5) + 1)

		for _, p := range postings {
			tf := float64(p.tf)
			dl := float64(r.docLen[p.docIdx])
			// BM25 score = IDF * (tf * (k1+1)) / (tf + k1 * (1 - b + b * dl/avgDL))
			score := idf * (tf * (r.k1 + 1)) / (tf + r.k1*(1-r.b+r.b*dl/r.avgDL))
			scores[p.docIdx] += score
		}
	}

	// 按分数降序排列
	type scored struct {
		idx   int
		score float64
	}
	ranked := make([]scored, 0, n)
	for i, s := range scores {
		if s > 0 {
			ranked = append(ranked, scored{idx: i, score: s})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	// 取 topK
	limit := r.topK
	if limit > len(ranked) {
		limit = len(ranked)
	}

	results := make([]*schema.Document, limit)
	for i := 0; i < limit; i++ {
		doc := r.documents[ranked[i].idx]
		// 将 BM25 分数存入 metadata
		docCopy := *doc
		if docCopy.MetaData == nil {
			docCopy.MetaData = make(map[string]any)
		}
		docCopy.MetaData["_bm25_score"] = ranked[i].score
		results[i] = &docCopy
	}

	return results, nil
}

// BM25Doc BM25 文档加载用的中间结构
type BM25Doc struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Reference string `json:"reference"`
}

// LoadBM25DocsFromFile 从 JSON 文件加载文档
func LoadBM25DocsFromFile(filePath string) ([]*BM25Doc, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("bm25: read %s: %w", filePath, err)
	}
	var docs []*BM25Doc
	if err := json.Unmarshal(data, &docs); err != nil {
		return nil, fmt.Errorf("bm25: parse %s: %w", filePath, err)
	}
	return docs, nil
}

// AppendParsedQuestions 将解析后的题目追加到 BM25 索引
func (r *BM25Retriever) AppendParsedQuestions(questions []struct {
	ID        string
	Content   string
	Reference string
}) {
	newDocs := make([]*schema.Document, len(questions))
	for i, q := range questions {
		newDocs[i] = &schema.Document{
			ID:      q.ID,
			Content: q.Content + "\n参考答案：" + q.Reference,
		}
	}
	// 追加到现有文档后重建索引
	r.documents = append(r.documents, newDocs...)
	allDocs := r.documents
	r.IndexDocuments(allDocs)
}

// IndexBM25Docs 从 BM25Doc 构建索引
func (r *BM25Retriever) IndexBM25Docs(docs []*BM25Doc) {
	schemaDocs := make([]*schema.Document, len(docs))
	for i, d := range docs {
		schemaDocs[i] = &schema.Document{
			ID:      d.ID,
			Content: d.Content + "\n参考答案：" + d.Reference,
		}
	}
	r.IndexDocuments(schemaDocs)
}

// BM25Manager 按用户管理 BM25 索引实例
type BM25Manager struct {
	mu         sync.RWMutex
	retrievers map[string]*BM25Retriever // userID -> retriever
	topK       int
}

// NewBM25Manager 创建 BM25 管理器
func NewBM25Manager(topK int) *BM25Manager {
	if topK <= 0 {
		topK = 10
	}
	return &BM25Manager{
		retrievers: make(map[string]*BM25Retriever),
		topK:       topK,
	}
}

// ReplaceDocuments 覆盖指定用户的题库索引
func (m *BM25Manager) ReplaceDocuments(userID string, docs []*schema.Document) {
	r := NewBM25Retriever(m.topK)
	r.IndexDocuments(docs)
	m.mu.Lock()
	m.retrievers[userID] = r
	m.mu.Unlock()
}

// AppendDocuments 追加文档到指定用户的索引
func (m *BM25Manager) AppendDocuments(userID string, docs []*schema.Document) {
	m.mu.Lock()
	r, ok := m.retrievers[userID]
	if !ok {
		r = NewBM25Retriever(m.topK)
		m.retrievers[userID] = r
	}
	m.mu.Unlock()
	r.documents = append(r.documents, docs...)
	r.IndexDocuments(r.documents)
}

// Retrieve 检索指定用户的题库
func (m *BM25Manager) Retrieve(ctx context.Context, userID string, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	m.mu.RLock()
	r, ok := m.retrievers[userID]
	m.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return r.Retrieve(ctx, query, opts...)
}

// DeleteByUserID 删除指定用户的索引
func (m *BM25Manager) DeleteByUserID(userID string) {
	m.mu.Lock()
	delete(m.retrievers, userID)
	m.mu.Unlock()
}

// tokenize 简单分词：按空格和标点切分，转小写
// 实际生产中可接入 jieba 等中文分词器
func tokenize(text string) []string {
	text = strings.ToLower(text)
	// 将常见标点替换为空格
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "、", " ", "：", " ", "；", " ",
		"？", " ", "！", " ", "（", " ", "）", " ",
		",", " ", ".", " ", ":", " ", ";", " ",
		"?", " ", "!", " ", "(", " ", ")", " ",
		"\n", " ", "\t", " ", "\r", " ",
	)
	text = replacer.Replace(text)

	tokens := strings.Fields(text)
	// 过滤空串和过短的词
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) > 0 {
			result = append(result, t)
		}
	}
	return result
}
