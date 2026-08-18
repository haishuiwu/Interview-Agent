/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package config

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewChatModel 创建通义千问 ChatModel（通过 OpenAI 兼容接口）
func NewChatModel(ctx context.Context, cfg *Config) (model.ChatModel, error) {
	temp := float32(0.7)
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     cfg.LLM.BaseURL,
		APIKey:      cfg.LLM.APIKey,
		Model:       cfg.LLM.Model,
		Temperature: &temp,
	})
	if err != nil {
		return nil, fmt.Errorf("config: create chat model: %w", err)
	}
	return chatModel, nil
}

// NewChatModelWithModel 创建指定模型的 ChatModel
func NewChatModelWithModel(ctx context.Context, cfg *Config, modelName string) (model.ChatModel, error) {
	temp := float32(0.7)
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     cfg.LLM.BaseURL,
		APIKey:      cfg.LLM.APIKey,
		Model:       modelName,
		Temperature: &temp,
	})
	if err != nil {
		return nil, fmt.Errorf("config: create chat model(%s): %w", modelName, err)
	}
	return chatModel, nil
}
