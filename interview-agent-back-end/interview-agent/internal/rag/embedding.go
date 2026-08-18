/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package rag 实现 RAG（检索增强生成）多路召回系统
package rag

import (
	"context"
	"fmt"

	embOpenAI "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/embedding"
)

// NewEmbedder 创建通义千问 Embedding 组件（通过 OpenAI 兼容接口）
func NewEmbedder(ctx context.Context, apiKey, baseURL, model string) (embedding.Embedder, error) {
	encodingFmt := embOpenAI.EmbeddingEncodingFormatFloat
	embedder, err := embOpenAI.NewEmbedder(ctx, &embOpenAI.EmbeddingConfig{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          model,
		EncodingFormat: &encodingFmt,
	})
	if err != nil {
		return nil, fmt.Errorf("rag: create embedder: %w", err)
	}
	return embedder, nil
}
