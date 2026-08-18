/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"interview-agent/internal/config"
	"interview-agent/internal/loader"
	"interview-agent/internal/rag"
)

// evalUserID 评估专用 userID，和业务用户隔离
const evalUserID = "eval_user"

// runEval RAG 离线评估子命令
//
// 两种模式：
//
//	--prepare  准备评估环境：解析 MD 题库 → 写入 Milvus/BM25 → 导出 manifest
//	默认       执行评估：加载 dataset → 检索 → 计算指标 → 输出报告
func runEval(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	prepare := fs.Bool("prepare", false, "准备评估环境：解析 MD 题库写入 Milvus/BM25，导出 manifest.json")
	genDataset := fs.Bool("gen-dataset", false, "基于 manifest 自动生成评估数据集（需先运行 --prepare）")
	datasetPath := fs.String("dataset", "data/eval/dataset_v1.json", "评估数据集路径")
	outDir := fs.String("out", "data/eval/reports", "报告输出目录")
	note := fs.String("note", "", "实验备注，写入报告 Config.Note，便于 A/B 对比")
	skipRerank := fs.Bool("skip-rerank", false, "跳过 LLM Rerank 阶段（用于 A/B 对比）")
	questionsDir := fs.String("questions", "data/questions", "题库 MD 文件目录（仅 --prepare 模式使用）")
	manifestPath := fs.String("manifest", "data/eval/manifest.json", "manifest 输出/读取路径")
	sampleCount := fs.Int("sample-count", 50, "自动生成数据集的样本数量（仅 --gen-dataset 使用）")
	if err := fs.Parse(args); err != nil {
		log.Fatalf("解析参数失败: %v", err)
	}

	if *prepare {
		runEvalPrepare(*questionsDir, *manifestPath)
	} else if *genDataset {
		runEvalGenDataset(*manifestPath, *datasetPath, *sampleCount)
	} else {
		runEvalExecute(*datasetPath, *outDir, *note, *skipRerank, *manifestPath)
	}
}

// ============================================================
// prepare 模式：解析 MD 题库 → 写入 Milvus/BM25 → 导出 manifest
// ============================================================

// manifestEntry manifest.json 中每条题目的结构
//
// content 存完整题目文本（BM25 索引需要），content_preview 存截断摘要（方便人看）。
type manifestEntry struct {
	ID             string   `json:"id"`
	ContentPreview string   `json:"content_preview"`          // 截断到 80 字符，方便人工标注时快速浏览
	Content        string   `json:"content"`                  // 完整题目文本（BM25 索引使用）
	Reference      string   `json:"reference,omitempty"`      // 参考答案
	Topic          string   `json:"topic"`
	Difficulty     string   `json:"difficulty"`
	Type           string   `json:"type"`
	Skills         []string `json:"skills"`
	SourceFile     string   `json:"source_file"`
}

func runEvalPrepare(questionsDir, manifestPath string) {
	ctx := context.Background()

	// 1. 扫描 MD 文件
	pattern := filepath.Join(questionsDir, "*", "*.md")
	mdFiles, err := filepath.Glob(pattern)
	if err != nil || len(mdFiles) == 0 {
		log.Fatalf("[Prepare] 未找到 MD 题库文件，请确认 %s 目录结构正确", questionsDir)
	}
	sort.Strings(mdFiles) // 保证顺序确定性
	fmt.Printf("[Prepare] 找到 %d 个 MD 题库文件\n", len(mdFiles))

	// 2. 初始化 LLM + Milvus
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	chatModel, err := config.NewChatModel(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 ChatModel 失败: %v", err)
	}

	embedder, err := rag.NewEmbedder(ctx, cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.EmbeddingModel)
	if err != nil {
		log.Fatalf("创建 Embedder 失败: %v", err)
	}
	milvusStore, err := rag.NewMilvusStore(ctx, cfg.Milvus.Addr, embedder, 10)
	if err != nil {
		log.Fatalf("连接 Milvus 失败: %v", err)
	}
	defer milvusStore.Close()

	// 3. 清除旧的 eval_user 数据
	fmt.Printf("[Prepare] 清除 eval_user 旧数据...\n")
	if err := milvusStore.DeleteByUserID(ctx, evalUserID); err != nil {
		log.Printf("[Prepare] 清除旧数据失败（可能无旧数据）: %v", err)
	}

	// 4. 逐文件解析 + 写入
	var allEntries []manifestEntry
	topicCounter := make(map[string]int) // 用于生成确定性 ID

	for _, mdFile := range mdFiles {
		baseName := filepath.Base(mdFile)
		// 从目录名提取 topic 前缀：go_interview → go, mysql_interview → mysql
		dirName := filepath.Base(filepath.Dir(mdFile))
		topicPrefix := strings.TrimSuffix(dirName, "_interview")

		fmt.Printf("\n[Prepare] 解析: %s\n", mdFile)

		// 读取文件内容
		rawText, err := loader.LoadFile(mdFile)
		if err != nil {
			log.Printf("[Prepare] 读取 %s 失败: %v，跳过", mdFile, err)
			continue
		}
		fmt.Printf("[Prepare] 文件长度: %d 字符\n", len(rawText))

		// LLM 解析
		fmt.Printf("[Prepare] LLM 解析中...\n")
		result, err := loader.ParseQuestionBank(ctx, chatModel, rawText)
		if err != nil {
			log.Printf("[Prepare] LLM 解析 %s 失败: %v，跳过", mdFile, err)
			continue
		}
		fmt.Printf("[Prepare] 解析完成: 识别 %d 道，通过 %d 道，失败 %d 道\n",
			result.Total, result.Success, result.Failed)

		if result.Success == 0 {
			continue
		}

		// 生成确定性 ID 并写入 Milvus
		milvusQuestions := make([]struct {
			ID         string
			Content    string
			Reference  string
			Type       string
			Difficulty string
			Skills     []string
		}, len(result.Questions))

		for i, q := range result.Questions {
			topicCounter[topicPrefix]++
			stableID := fmt.Sprintf("eval_%s_%03d", topicPrefix, topicCounter[topicPrefix])

			milvusQuestions[i] = struct {
				ID         string
				Content    string
				Reference  string
				Type       string
				Difficulty string
				Skills     []string
			}{
				ID: stableID, Content: q.Content, Reference: q.Reference,
				Type: q.Type, Difficulty: q.Difficulty, Skills: q.Skills,
			}

			// 构造 manifest 条目
			preview := q.Content
			if len([]rune(preview)) > 80 {
				preview = string([]rune(preview)[:80]) + "..."
			}
			allEntries = append(allEntries, manifestEntry{
				ID:             stableID,
				ContentPreview: preview,
				Content:        q.Content,
				Reference:      q.Reference,
				Topic:          topicPrefix,
				Difficulty:     q.Difficulty,
				Type:           q.Type,
				Skills:         q.Skills,
				SourceFile:     baseName,
			})
		}

		if err := milvusStore.LoadParsedQuestions(ctx, evalUserID, baseName, milvusQuestions); err != nil {
			log.Printf("[Prepare] 写入 Milvus 失败 (%s): %v", mdFile, err)
			continue
		}
		fmt.Printf("[Prepare] ✓ %s: %d 道题写入 Milvus\n", baseName, len(milvusQuestions))
	}

	if len(allEntries) == 0 {
		log.Fatalf("[Prepare] 没有成功解析任何题目，请检查 MD 文件和 LLM 配置")
	}

	// 5. 导出 manifest.json
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		log.Fatalf("创建目录失败: %v", err)
	}
	manifestData, err := json.MarshalIndent(allEntries, "", "  ")
	if err != nil {
		log.Fatalf("序列化 manifest 失败: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		log.Fatalf("写入 manifest 失败: %v", err)
	}

	fmt.Printf("\n======== Prepare 完成 ========\n")
	fmt.Printf("题目总数:   %d\n", len(allEntries))
	for topic, count := range topicCounter {
		fmt.Printf("  %s: %d 道\n", topic, count)
	}
	fmt.Printf("Manifest:   %s\n", manifestPath)
	fmt.Printf("\n下一步：基于 manifest 中的 ID 标注评估数据集 data/eval/dataset_v1.json\n")
}

// ============================================================
// eval 默认模式：加载数据集 → 检索 → 计算指标 → 输出报告
// ============================================================

func runEvalExecute(datasetPath, outDir, note string, skipRerank bool, manifestPath string) {
	ctx := context.Background()

	// 1. 加载评估数据集
	samples, err := rag.LoadEvalDataset(datasetPath)
	if err != nil {
		log.Fatalf("加载数据集失败: %v", err)
	}
	fmt.Printf("[Eval] 数据集加载完成: %s (样本数: %d)\n", datasetPath, len(samples))

	// 2. 加载配置 + 初始化 Milvus
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	embedder, err := rag.NewEmbedder(ctx, cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.EmbeddingModel)
	if err != nil {
		log.Fatalf("创建 Embedder 失败: %v", err)
	}
	fmt.Printf("[Eval] Embedder 就绪: %s\n", cfg.LLM.EmbeddingModel)

	milvusStore, err := rag.NewMilvusStore(ctx, cfg.Milvus.Addr, embedder, 20)
	if err != nil {
		log.Fatalf("连接 Milvus 失败: %v", err)
	}
	defer milvusStore.Close()
	fmt.Printf("[Eval] Milvus 已连接: %s\n", cfg.Milvus.Addr)

	// 3. 从 manifest 加载 BM25 索引（不再依赖本地 JSON 题库文件）
	bm25Manager := rag.NewBM25Manager(10)
	loadBM25FromManifest(bm25Manager, manifestPath)
	fmt.Printf("[Eval] BM25 索引就绪 (userID=%s)\n", evalUserID)

	// 4. 初始化 Reranker（可关闭以做 A/B）
	var reranker rag.RerankStrategy
	rerankerType := "none"
	if !skipRerank {
		switch cfg.LLM.RerankerType {
		case "cross-encoder":
			reranker = rag.NewCrossEncoderReranker(cfg.LLM.APIKey, cfg.LLM.RerankModel, 20) // 评估用 20，避免 rerank 截断压低 Recall@20
			rerankerType = "cross-encoder"
			fmt.Printf("[Eval] Reranker (cross-encoder) 就绪: %s\n", cfg.LLM.RerankModel)
		default:
			chatModel, err := config.NewChatModel(ctx, cfg)
			if err != nil {
				log.Fatalf("创建 ChatModel 失败: %v", err)
			}
			reranker = rag.NewReranker(chatModel, 20) // 评估用 20，避免 rerank 截断压低 Recall@20
			rerankerType = "llm"
			fmt.Printf("[Eval] Reranker (LLM) 就绪: %s\n", cfg.LLM.Model)
		}
	} else {
		fmt.Println("[Eval] 已跳过 Reranker")
	}

	// 5. 构造 RAGConfig 快照
	ragCfg := rag.RAGConfig{
		EmbeddingModel: cfg.LLM.EmbeddingModel,
		VectorDim:      rag.VectorDimension,
		VectorTopK:     20,
		BM25TopK:       20,
		BM25K1:         1.5,
		BM25B:          0.75,
		RerankerType:   rerankerType,
		RerankTopN:     20,
		Note:           note,
	}

	// 6. 跑评估
	fmt.Println("[Eval] 开始评估...")
	report, err := rag.RunEvaluation(ctx, &rag.RunEvaluationConfig{
		Dataset:     samples,
		UserID:      evalUserID,
		MilvusStore: milvusStore,
		BM25Manager: bm25Manager,
		Reranker:    reranker,
		RAGConfig:   ragCfg,
	})
	if err != nil {
		log.Fatalf("评估失败: %v", err)
	}
	report.DatasetPath = datasetPath

	// 7. 输出报告（JSON + Markdown）
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}
	timestamp := time.Now().Format("20060102_150405")
	jsonPath := filepath.Join(outDir, fmt.Sprintf("eval_report_%s.json", timestamp))
	mdPath := filepath.Join(outDir, fmt.Sprintf("eval_report_%s.md", timestamp))

	if err := rag.SaveReportJSON(report, jsonPath); err != nil {
		log.Fatalf("保存 JSON 报告失败: %v", err)
	}
	if err := rag.SaveReportMarkdown(report, mdPath); err != nil {
		log.Fatalf("保存 Markdown 报告失败: %v", err)
	}

	fmt.Println()
	fmt.Println("======== 评估完成 ========")
	fmt.Printf("样本数:     %d\n", report.SampleCount)
	fmt.Printf("耗时:       %s\n", report.Duration)
	fmt.Printf("Recall@10:  %.4f\n", report.Overall.RecallAt10)
	fmt.Printf("Recall@20:  %.4f\n", report.Overall.RecallAt20)
	fmt.Printf("MRR:        %.4f\n", report.Overall.MRR)
	fmt.Println()
	fmt.Printf("JSON 报告:     %s\n", jsonPath)
	fmt.Printf("Markdown 报告: %s\n", mdPath)
}

// ============================================================
// gen-dataset 模式：基于 manifest 自动生成评估数据集
// ============================================================

func runEvalGenDataset(manifestPath, datasetPath string, targetCount int) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Fatalf("[GenDataset] 读取 manifest 失败: %v\n请先运行 eval --prepare 生成 manifest.json", err)
	}

	var entries []manifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Fatalf("[GenDataset] 解析 manifest 失败: %v", err)
	}
	if len(entries) == 0 {
		log.Fatalf("[GenDataset] manifest 为空，请先运行 eval --prepare")
	}

	// 按 topic 分组
	topicGroups := make(map[string][]manifestEntry)
	for _, e := range entries {
		topicGroups[e.Topic] = append(topicGroups[e.Topic], e)
	}

	// 按 topic 名排序保证确定性
	topics := make([]string, 0, len(topicGroups))
	for t := range topicGroups {
		topics = append(topics, t)
	}
	sort.Strings(topics)

	// 每个 topic 分配的采样数
	perTopic := targetCount / len(topics)
	if perTopic < 3 {
		perTopic = 3
	}

	var samples []rag.EvalSample
	sampleIdx := 0

	for _, topic := range topics {
		group := topicGroups[topic]
		displayName := topicDisplayName(topic)

		// 均匀采样：步长 = 总数/目标数，至少为 1
		step := len(group) / perTopic
		if step < 1 {
			step = 1
		}

		count := 0
		for i := 0; i < len(group) && count < perTopic; i += step {
			entry := group[i]
			sampleIdx++

			query := generateQueryFromEntry(entry)
			relevantIDs := findRelatedEntries(entry, group)

			difficulty := entry.Difficulty
			if difficulty == "" {
				difficulty = "medium"
			}

			samples = append(samples, rag.EvalSample{
				ID:             fmt.Sprintf("eval_%03d", sampleIdx),
				Query:          query,
				RelevantDocIDs: relevantIDs,
				Topic:          displayName,
				Difficulty:     difficulty,
				Note:           fmt.Sprintf("自动生成，种子题: %s", entry.ID),
			})
			count++
		}
	}

	// 写出
	out, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		log.Fatalf("[GenDataset] 序列化失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(datasetPath), 0755); err != nil {
		log.Fatalf("[GenDataset] 创建目录失败: %v", err)
	}
	if err := os.WriteFile(datasetPath, out, 0644); err != nil {
		log.Fatalf("[GenDataset] 写入失败: %v", err)
	}

	fmt.Printf("\n======== 评估数据集生成完成 ========\n")
	fmt.Printf("样本总数:   %d\n", len(samples))
	for _, t := range topics {
		displayName := topicDisplayName(t)
		count := 0
		for _, s := range samples {
			if s.Topic == displayName {
				count++
			}
		}
		fmt.Printf("  %s: %d 条\n", displayName, count)
	}
	fmt.Printf("输出路径:   %s\n", datasetPath)
	fmt.Printf("\n下一步：运行 go run ./cmd/ eval -note baseline 执行评估\n")
}

// generateQueryFromEntry 从 manifest 条目生成 SearchQuery 风格的查询
//
// 模拟 Phase 1 LLM 输出的 SearchQuery：关键词组合，不是完整问句。
// 策略：取题目文本，去掉问句前缀和标点，截断到合理长度。
func generateQueryFromEntry(entry manifestEntry) string {
	content := entry.Content

	// 去掉常见问句前缀
	prefixes := []string{
		"请详细描述", "请详细解释", "请详细说明",
		"请描述一下", "请解释一下", "请说明一下",
		"请描述", "请解释", "请说明", "请简述", "请列举",
		"详细描述", "详细解释", "详细说明",
		"简述", "描述", "解释", "说明",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(content, p) {
			content = strings.TrimPrefix(content, p)
			break
		}
	}

	// 标点替换为空格
	content = strings.Map(func(r rune) rune {
		switch r {
		case '？', '?', '。', '！', '!', '（', '）', '(', ')', '、', '，', ',', '；', ';', '：', ':', '\n', '\r':
			return ' '
		default:
			return r
		}
	}, content)

	// 合并多余空格
	fields := strings.Fields(content)
	content = strings.Join(fields, " ")

	// 截断到合理长度（SearchQuery 风格通常 10-30 个汉字 + 英文术语）
	runes := []rune(content)
	if len(runes) > 35 {
		content = string(runes[:35])
	}

	return strings.TrimSpace(content)
}

// findRelatedEntries 找出和种子题相关的文档 ID
//
// 策略：种子题本身一定相关 + 同 topic 中 skill 重叠 ≥2 的题目（最多取 2 个）。
func findRelatedEntries(seed manifestEntry, group []manifestEntry) []string {
	result := []string{seed.ID}

	if len(seed.Skills) < 2 {
		return result
	}

	// 种子的 skill 集合（小写归一化）
	seedSkills := make(map[string]bool)
	for _, s := range seed.Skills {
		seedSkills[strings.ToLower(strings.TrimSpace(s))] = true
	}

	type candidate struct {
		id      string
		overlap int
	}
	var candidates []candidate

	for _, e := range group {
		if e.ID == seed.ID {
			continue
		}
		overlap := 0
		for _, s := range e.Skills {
			if seedSkills[strings.ToLower(strings.TrimSpace(s))] {
				overlap++
			}
		}
		if overlap >= 2 {
			candidates = append(candidates, candidate{e.ID, overlap})
		}
	}

	// 按重叠度降序，取前 2 个
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].overlap > candidates[j].overlap
	})
	limit := 2
	if limit > len(candidates) {
		limit = len(candidates)
	}
	for i := 0; i < limit; i++ {
		result = append(result, candidates[i].id)
	}

	return result
}

// topicDisplayName 将 manifest 中的 topic 前缀转为可读名称
func topicDisplayName(topic string) string {
	switch topic {
	case "go":
		return "Go"
	case "mysql":
		return "MySQL"
	case "redis":
		return "Redis"
	case "distributed":
		return "分布式系统"
	case "mq":
		return "消息队列"
	default:
		return topic
	}
}

// loadBM25FromManifest 从 manifest.json 加载题目到 BM25 索引
//
// manifest 是 prepare 阶段生成的，包含完整的题目 ID、内容和参考答案。
// 这样 eval 阶段不再依赖 data/questions/*.json 这种本地文件。
func loadBM25FromManifest(bm25Manager *rag.BM25Manager, manifestPath string) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Printf("[BM25] 读取 manifest 失败: %v", err)
		log.Println("[BM25] 请先运行 eval --prepare 生成 manifest.json")
		return
	}

	var entries []manifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("[BM25] 解析 manifest 失败: %v", err)
		return
	}

	if len(entries) == 0 {
		log.Println("[BM25] manifest 为空，BM25 检索将不可用")
		return
	}

	// 构造 schema.Document 并写入 BM25（用完整 content + reference，和 Milvus 写入格式一致）
	docs := make([]*schema.Document, len(entries))
	for i, e := range entries {
		content := e.Content
		if e.Reference != "" {
			content += "\n参考答案：" + e.Reference
		}
		docs[i] = &schema.Document{
			ID:      e.ID,
			Content: content,
		}
	}

	bm25Manager.ReplaceDocuments(evalUserID, docs)
	log.Printf("[BM25] 从 manifest 加载完成: %d 篇文档 (userID=%s)", len(docs), evalUserID)
}
