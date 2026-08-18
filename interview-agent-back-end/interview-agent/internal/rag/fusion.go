/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"context"
	"sort"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// RRFConstant RRF 融合算法的常数 k，默认 60
const RRFConstant = 60

// MultiRetriever 多路召回融合检索器，使用 RRF（Reciprocal Rank Fusion）算法
type MultiRetriever struct {
	retrievers []retriever.Retriever
	topK       int
	k          int // RRF 常数
}

// NewMultiRetriever 创建多路召回融合检索器
func NewMultiRetriever(retrievers []retriever.Retriever, topK int) *MultiRetriever {
	if topK <= 0 {
		topK = 10
	}
	return &MultiRetriever{
		retrievers: retrievers,
		topK:       topK,
		k:          RRFConstant,
	}
}

// Retrieve 多路召回 + RRF 融合
func (m *MultiRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	// 从每个检索器获取结果
	allResults := make([][]*schema.Document, len(m.retrievers))
	for i, r := range m.retrievers {
		docs, err := r.Retrieve(ctx, query, opts...)
		if err != nil {
			// 某一路失败不影响其他路
			continue
		}
		allResults[i] = docs
	}

	// RRF 融合
	return m.rrfFusion(allResults), nil
}

// rrfFusion RRF（Reciprocal Rank Fusion）融合算法
// 对于每个文档，RRF 分数 = sum(1 / (k + rank_i))，其中 rank_i 是文档在第 i 路中的排名
func (m *MultiRetriever) rrfFusion(allResults [][]*schema.Document) []*schema.Document {
	// docID -> 融合分数
	scoreMap := make(map[string]float64)
	// docID -> 文档（保留最后看到的版本）
	docMap := make(map[string]*schema.Document)

	for _, results := range allResults {
		for rank, doc := range results {
			id := docID(doc)
			scoreMap[id] += 1.0 / float64(m.k+rank+1) // rank 从 0 开始，+1 转为从 1 开始
			docMap[id] = doc
		}
	}

	// 按 RRF 分数降序排列
	type scored struct {
		id    string
		score float64
	}
	ranked := make([]scored, 0, len(scoreMap))
	for id, score := range scoreMap {
		ranked = append(ranked, scored{id: id, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	// 取 topK
	limit := m.topK
	if limit > len(ranked) {
		limit = len(ranked)
	}

	results := make([]*schema.Document, limit)
	for i := 0; i < limit; i++ {
		doc := docMap[ranked[i].id]
		docCopy := *doc
		if docCopy.MetaData == nil {
			docCopy.MetaData = make(map[string]any)
		}
		docCopy.MetaData["_rrf_score"] = ranked[i].score
		results[i] = &docCopy
	}

	return results
}

// docID 获取文档唯一标识
func docID(doc *schema.Document) string {
	if doc.ID != "" {
		return doc.ID
	}
	// 没有 ID 时用内容前 100 个字符作为标识
	if len(doc.Content) > 100 {
		return doc.Content[:100]
	}
	return doc.Content
}
