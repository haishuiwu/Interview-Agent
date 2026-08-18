/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	milvusIndexer "github.com/cloudwego/eino-ext/components/indexer/milvus"
	milvusRetriever "github.com/cloudwego/eino-ext/components/retriever/milvus"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const (
	// CollectionName Milvus 集合名称
	CollectionName = "interview_questions"
	// VectorDimension 通义千问 text-embedding-v3 的向量维度
	VectorDimension = 1024
)

// milvusRow 自定义 Milvus 行结构，向量使用 []float32 匹配 FieldTypeFloatVector
type milvusRow struct {
	ID         string    `json:"id" milvus:"name:id"`
	Content    string    `json:"content" milvus:"name:content"`
	Vector     []float32 `json:"vector" milvus:"name:vector"`
	Metadata   []byte    `json:"metadata" milvus:"name:metadata"`
	UserID     string    `json:"user_id" milvus:"name:user_id"`
	SourceFile string    `json:"source_file" milvus:"name:source_file"`
}

// floatVectorConverter 将文档和向量转换为 Milvus 行数据（[]float32 格式）
func floatVectorConverter(_ context.Context, docs []*schema.Document, vectors [][]float64) ([]interface{}, error) {
	rows := make([]interface{}, 0, len(docs))
	for idx, doc := range docs {
		metadata, err := sonic.Marshal(doc.MetaData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal metadata: %w", err)
		}
		vec32 := make([]float32, len(vectors[idx]))
		for i, v := range vectors[idx] {
			vec32[i] = float32(v)
		}
		userID, _ := doc.MetaData["user_id"].(string)
		sourceFile, _ := doc.MetaData["source_file"].(string)
		rows = append(rows, &milvusRow{
			ID:         doc.ID,
			Content:    doc.Content,
			Vector:     vec32,
			Metadata:   metadata,
			UserID:     userID,
			SourceFile: sourceFile,
		})
	}
	return rows, nil
}

// MilvusStore 基于 Milvus 的向量存储，封装 Eino 官方 indexer + retriever
type MilvusStore struct {
	client       client.Client
	countQuerier milvusCountQuerier
	embedder     embedding.Embedder
	indexer      *milvusIndexer.Indexer
	retriever    retriever.Retriever
}

type milvusCountQuerier interface {
	Query(ctx context.Context, collectionName string, partitionNames []string, expr string, outputFields []string, opts ...client.SearchQueryOptionFunc) (client.ResultSet, error)
}

// milvusInitTimeout Milvus Indexer/Retriever 初始化超时时间
// eino-ext 的 NewIndexer 会自动 LoadCollection（把数据从磁盘加载到内存），
// Milvus standalone 重启后可能因节点 ID 变更导致 load 永久卡住，所以必须加超时。
const milvusInitTimeout = 30 * time.Second

// NewMilvusStore 创建 Milvus 向量存储
func NewMilvusStore(ctx context.Context, addr string, embedder embedding.Embedder, topK int) (*MilvusStore, error) {
	// 1. 创建 Milvus 客户端
	cli, err := client.NewClient(ctx, client.Config{
		Address: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("milvus: connect to %s: %w", addr, err)
	}
	log.Printf("[Milvus] 已连接: %s", addr)

	// 2. 创建 indexer（用于写入文档）
	// 使用超时 context，防止 eino-ext 内部 LoadCollection 卡住
	initCtx, cancel := context.WithTimeout(ctx, milvusInitTimeout)
	defer cancel()

	type indexerResult struct {
		idx *milvusIndexer.Indexer
		err error
	}
	idxCh := make(chan indexerResult, 1)
	go func() {
		idx, err := milvusIndexer.NewIndexer(initCtx, &milvusIndexer.IndexerConfig{
			Client:     cli,
			Collection: CollectionName,
			Fields: []*entity.Field{
				entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(256).WithIsPrimaryKey(true),
				entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(8192),
				entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(VectorDimension),
				entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON),
				entity.NewField().WithName("user_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(128),
				entity.NewField().WithName("source_file").WithDataType(entity.FieldTypeVarChar).WithMaxLength(256),
			},
			MetricType:        milvusIndexer.COSINE,
			Embedding:         embedder,
			DocumentConverter: floatVectorConverter,
		})
		idxCh <- indexerResult{idx, err}
	}()

	var idx *milvusIndexer.Indexer
	select {
	case res := <-idxCh:
		if res.err != nil {
			cli.Close()
			return nil, fmt.Errorf("milvus: create indexer: %w", res.err)
		}
		idx = res.idx
	case <-initCtx.Done():
		cli.Close()
		log.Println("============================================================")
		log.Println("[Milvus] collection 加载超时！")
		log.Println("这是 Milvus standalone 的已知问题：重启后节点 ID 变更")
		log.Println("导致 LoadCollection 永久卡住。")
		log.Println("")
		log.Println("请执行以下命令清空 Milvus 数据后重启：")
		log.Println("  docker compose down")
		log.Println("  docker volume rm interview-agent_milvus_data")
		log.Println("  docker compose up -d")
		log.Println("")
		log.Println("注意：清空后需要重新上传题库。")
		log.Println("============================================================")
		return nil, fmt.Errorf("milvus: collection 加载超时（30s），请清空 Milvus 数据后重启，详见上方日志")
	}
	log.Printf("[Milvus] Indexer 就绪，集合: %s", CollectionName)

	// 3. 创建 retriever（用于检索）
	if topK <= 0 {
		topK = 10
	}
	// 显式设置 SearchParam，避免 eino-ext 默认的 defaultSearchParam 用向量维度当 radius 导致
	// COSINE 度量下 "range_filter > radius" 断言失败
	sp, _ := entity.NewIndexAUTOINDEXSearchParam(1)

	ret, err := milvusRetriever.NewRetriever(ctx, &milvusRetriever.RetrieverConfig{
		Client:     cli,
		Collection: CollectionName,
		TopK:       topK,
		MetricType: entity.COSINE,
		Embedding:  embedder,
		Sp:         sp,
		VectorConverter: func(_ context.Context, vectors [][]float64) ([]entity.Vector, error) {
			result := make([]entity.Vector, 0, len(vectors))
			for _, vec := range vectors {
				fv := make(entity.FloatVector, len(vec))
				for i, v := range vec {
					fv[i] = float32(v)
				}
				result = append(result, fv)
			}
			return result, nil
		},
		OutputFields: []string{
			"id", "content", "metadata",
		},
	})
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("milvus: create retriever: %w", err)
	}
	log.Printf("[Milvus] Retriever 就绪，TopK: %d", topK)

	return &MilvusStore{
		client:       cli,
		countQuerier: cli,
		embedder:     embedder,
		indexer:      idx,
		retriever:    ret,
	}, nil
}

// Store 将文档写入 Milvus（自动分批，每批最多 10 条，避免 Embedding 模型批量限制）
func (s *MilvusStore) Store(ctx context.Context, docs []*schema.Document) ([]string, error) {
	const batchSize = 10
	var allIDs []string

	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[i:end]
		ids, err := s.indexer.Store(ctx, batch)
		if err != nil {
			return allIDs, fmt.Errorf("milvus: store batch %d-%d: %w", i, end, err)
		}
		allIDs = append(allIDs, ids...)
		log.Printf("[Milvus] 写入批次 %d-%d，%d 篇文档", i, end, len(ids))
	}

	log.Printf("[Milvus] 共写入 %d 篇文档", len(allIDs))
	return allIDs, nil
}

// Retrieve 从 Milvus 检索文档（实现 retriever.Retriever 接口）
func (s *MilvusStore) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	return s.retriever.Retrieve(ctx, query, opts...)
}

// Close 关闭 Milvus 连接
func (s *MilvusStore) Close() error {
	return s.client.Close()
}

// DeleteByUserID 删除指定用户的所有题目
func (s *MilvusStore) DeleteByUserID(ctx context.Context, userID string) error {
	expr := fmt.Sprintf(`user_id == "%s"`, userID)
	if err := s.client.Delete(ctx, CollectionName, "", expr); err != nil {
		return fmt.Errorf("milvus: delete by user_id %s: %w", userID, err)
	}
	log.Printf("[Milvus] 已删除用户 %s 的所有题目", userID)
	return nil
}

// DeleteBySourceFile 删除指定用户某个来源文件的题目（用于题库更新替换）
func (s *MilvusStore) DeleteBySourceFile(ctx context.Context, userID, sourceFile string) error {
	expr := fmt.Sprintf(`user_id == "%s" && source_file == "%s"`, userID, sourceFile)
	if err := s.client.Delete(ctx, CollectionName, "", expr); err != nil {
		return fmt.Errorf("milvus: delete by source_file: %w", err)
	}
	log.Printf("[Milvus] 已删除用户 %s 文件 %s 的题目", userID, sourceFile)
	return nil
}

// RetrieveByUser 检索指定用户的题目
func (s *MilvusStore) RetrieveByUser(ctx context.Context, userID string, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	filter := fmt.Sprintf("user_id == %s", strconv.Quote(userID))
	if s.countQuerier != nil {
		hasRows, err := milvusFilterHasRows(ctx, s.countQuerier, filter)
		if err == nil && !hasRows {
			return nil, nil
		}
	}
	opts = append(opts, milvusRetriever.WithFilter(filter))
	return s.retriever.Retrieve(ctx, query, opts...)
}

func milvusFilterHasRows(ctx context.Context, querier milvusCountQuerier, filter string) (bool, error) {
	const countField = "count(*)"
	result, err := querier.Query(
		ctx,
		CollectionName,
		nil,
		filter,
		[]string{countField},
		client.WithSearchQueryConsistencyLevel(entity.ClStrong),
	)
	if err != nil {
		return false, err
	}
	column, ok := result.GetColumn(countField).(*entity.ColumnInt64)
	if !ok || len(column.Data()) == 0 {
		return false, fmt.Errorf("milvus: count query returned no %s value", countField)
	}
	return column.Data()[0] > 0, nil
}

// LoadParsedQuestions 将解析后的结构化题目写入 Milvus（带用户隔离 + 文件来源标记）
func (s *MilvusStore) LoadParsedQuestions(ctx context.Context, userID, sourceFile string, questions []struct {
	ID         string
	Content    string
	Reference  string
	Type       string
	Difficulty string
	Skills     []string
}) error {
	docs := make([]*schema.Document, 0, len(questions))
	for _, q := range questions {
		docs = append(docs, &schema.Document{
			ID:      q.ID,
			Content: q.Content + "\n参考答案：" + q.Reference,
			MetaData: map[string]any{
				"type":        q.Type,
				"difficulty":  q.Difficulty,
				"skills":      q.Skills,
				"reference":   q.Reference,
				"user_id":     userID,
				"source_file": sourceFile,
			},
		})
	}
	_, err := s.Store(ctx, docs)
	return err
}

// LoadQuestionsFromFile 从 JSON 文件加载面试题到 Milvus（带用户隔离）
func (s *MilvusStore) LoadQuestionsFromFile(ctx context.Context, userID string, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("milvus: read file %s: %w", filePath, err)
	}

	var questions []struct {
		ID         string   `json:"id"`
		Content    string   `json:"content"`
		Type       string   `json:"type"`
		Difficulty string   `json:"difficulty"`
		Skills     []string `json:"skills"`
		FollowUps  []string `json:"follow_ups"`
		Reference  string   `json:"reference"`
	}
	if err := json.Unmarshal(data, &questions); err != nil {
		return fmt.Errorf("milvus: parse file %s: %w", filePath, err)
	}

	docs := make([]*schema.Document, 0, len(questions))
	for _, q := range questions {
		docs = append(docs, &schema.Document{
			ID:      q.ID,
			Content: q.Content + "\n参考答案：" + q.Reference,
			MetaData: map[string]any{
				"type":        q.Type,
				"difficulty":  q.Difficulty,
				"skills":      q.Skills,
				"follow_ups":  q.FollowUps,
				"reference":   q.Reference,
				"user_id":     userID,
				"source_file": filePath,
			},
		})
	}

	_, err = s.Store(ctx, docs)
	return err
}
