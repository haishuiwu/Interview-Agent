/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Diagnosis
 */

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"

	"interview-agent/internal/agent"
	"interview-agent/internal/auth"
	"interview-agent/internal/config"
	"interview-agent/internal/graph"
	"interview-agent/internal/handler"
	"interview-agent/internal/loader"
	"interview-agent/internal/mcp"
	"interview-agent/internal/memory"
	"interview-agent/internal/rag"
	"interview-agent/internal/skill"
	"interview-agent/internal/speech"
	"interview-agent/internal/speech/dashscope"
)

func main() {
	fmt.Println("=== Student-Coach｜AI 自适应学习训练与知识掌握诊断系统 ===")
	fmt.Println()

	if len(os.Args) < 2 {
		// 默认进入聊天模式
		runChat()
		return
	}

	switch os.Args[1] {
	case "chat":
		runChat()
	case "train", "interview": // interview 仅为历史 CLI 别名
		runTrainingCLI()
	case "load-data":
		runLoadData()
	case "web":
		runWeb()
	case "eval":
		runEval(os.Args[2:])
	default:
		printUsage()
	}
}

// ============================================================
// chat 命令：统一聊天入口（路由 + Chat Agent + Student-Coach）。
// ============================================================

func runChat() {
	ctx := context.Background()

	// ====== 1. 加载配置 ======
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	fmt.Printf("模型: %s\n", cfg.LLM.Model)

	// ====== 2. 初始化 MCP ======
	if err := loader.InitWebScraper(); err != nil {
		log.Printf("[MCP] Playwright 启动失败（URL 抓取不可用）: %v", err)
	} else {
		defer loader.CloseWebScraper()
		fmt.Println("[MCP] Playwright Web Scraper 就绪")
	}

	// ====== 3. 初始化 ChatModel ======
	chatModel, err := config.NewChatModel(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 ChatModel 失败: %v", err)
	}
	fmt.Println("[ChatModel] 就绪")

	// ====== 4. 连接基础设施 ======
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("连接 Redis 失败 (%s): %v", cfg.Redis.Addr, err)
	}
	defer rdb.Close()
	fmt.Printf("[Redis] 已连接: %s\n", cfg.Redis.Addr)

	mysqlStore, err := memory.NewMySQLStore(cfg.MySQL.DSN)
	if err != nil {
		log.Fatalf("连接 MySQL 失败: %v", err)
	}
	fmt.Println("[MySQL] 已连接，表结构就绪")

	redisStore := memory.NewRedisStore(rdb)
	combinedStore := memory.NewCombinedStore(redisStore, mysqlStore)

	embedder, err := rag.NewEmbedder(ctx, cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.EmbeddingModel)
	if err != nil {
		log.Fatalf("创建 Embedder 失败: %v", err)
	}
	fmt.Printf("[Embedder] 就绪: %s\n", cfg.LLM.EmbeddingModel)

	milvusStore, err := rag.NewMilvusStore(ctx, cfg.Milvus.Addr, embedder, 10)
	if err != nil {
		log.Fatalf("连接 Milvus 失败 (%s): %v", cfg.Milvus.Addr, err)
	}
	defer milvusStore.Close()

	bm25Manager := rag.NewBM25Manager(10)
	loadBuiltInStudentAbilityQuestions(bm25Manager)

	// ====== 5. 初始化 GitHub MCP（可选） ======
	var githubSearcher *mcp.GitHubSearcher
	if cfg.GitHub.Token != "" {
		gs, err := mcp.NewGitHubSearcher(cfg.GitHub.Token)
		if err != nil {
			log.Printf("[MCP] GitHub Searcher 启动失败（学习路径计划将使用 LLM 生成资源）: %v", err)
		} else {
			githubSearcher = gs
			fmt.Println("[MCP] GitHub Searcher 就绪")
		}
	}

	// ====== 6. 初始化 Agent 和路由器 ======
	chatAgent := agent.NewChatAgent(chatModel)
	router := agent.NewIntentRouter()
	orchestrator := graph.NewOrchestrator(&graph.OrchestratorConfig{
		ChatModel:      chatModel,
		Store:          combinedStore,
		MilvusStore:    milvusStore,
		BM25Manager:    bm25Manager,
		MySQLStore:     mysqlStore,
		GitHubSearcher: githubSearcher,
		RerankerType:   cfg.LLM.RerankerType,
		RerankModel:    cfg.LLM.RerankModel,
		APIKey:         cfg.LLM.APIKey,
	})

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("系统就绪！你可以：")
	fmt.Println("  - 交流学习目标、知识疑问与薄弱点")
	fmt.Println("  - 说「开始训练」启动自适应学习训练")
	fmt.Println("  - 粘贴课程标准、考试大纲或能力要求链接")
	fmt.Println("  - 输入 quit 退出系统")
	fmt.Println(strings.Repeat("=", 60))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var chatHistory []*schema.Message
	isTraining := false

	for {
		fmt.Print("\n你: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "quit" || strings.ToLower(input) == "exit" {
			fmt.Println("\n再见！祝你的学习能力持续进步！")
			break
		}

		intent := router.Route(input, isTraining)

		switch intent {
		case agent.IntentStartTraining:
			if !isTraining {
				fmt.Println("\n好的，开始准备本轮自适应学习训练！")
				// 收集学习目标与能力标准。
				abilityStandardText := collectAbilityStandard(ctx, chatModel)
				if abilityStandardText == "" {
					fmt.Println("目标标准为空，取消训练。继续聊天吧。")
					continue
				}
				fmt.Printf("\n[目标标准] 已加载，长度: %d 字符\n", len(abilityStandardText))

				// 收集学生画像。
				studentProfileText := collectStudentProfile()
				if studentProfileText == "" {
					fmt.Println("学生画像为空，取消训练。继续聊天吧。")
					continue
				}
				fmt.Printf("[学生画像] 已加载，长度: %d 字符\n", len(studentProfileText))

				// 开始训练。
				fmt.Println("\n" + strings.Repeat("=", 60))
				fmt.Println("学习状态分析与训练规划中，请稍候...")
				fmt.Println(strings.Repeat("=", 60))

				isTraining = true

				trainingScanner := bufio.NewScanner(os.Stdin)
				trainingScanner.Buffer(make([]byte, 1024*1024), 1024*1024)

				callbacks := &graph.TrainingCallbacks{
					OnStageChange: func(stage string, msg string) {
						fmt.Printf("\n[%s] %s\n", stage, msg)
					},
					OnQuestion: func(questionNum int, content string) {
						fmt.Printf("\n%s\n", strings.Repeat("-", 40))
						fmt.Printf("Student-Coach（第 %d 个训练任务）：\n\n%s\n\n", questionNum, content)
					},
					OnScore: func(score *agent.AnswerScore) {
						fmt.Printf("\n[评分: %.0f/100] %s\n", score.Score, score.Feedback)
					},
					OnReport: func(report string) {
						fmt.Println("\n" + strings.Repeat("=", 60))
						fmt.Println(report)
					},
					OnReviewPlan: func(plan string) {
						fmt.Println("\n" + strings.Repeat("=", 60))
						fmt.Println(plan)
					},
					GetUserAnswer: func() (string, error) {
						fmt.Print("你的回答（输入 END 结束本题，输入 quit 终止训练）：\n")
						answer := readMultiLine(trainingScanner)
						trimmed := strings.TrimSpace(strings.ToLower(answer))
						if trimmed == "quit" || trimmed == "exit" {
							return "", graph.ErrUserQuit
						}
						return answer, nil
					},
				}

				_, err := orchestrator.RunTraining(ctx, abilityStandardText, studentProfileText, "default_user", callbacks)
				if err != nil {
					fmt.Printf("\n训练流程出错: %v\n", err)
				}

				isTraining = false
				fmt.Println("\n本轮训练结束！你可以继续交流，或者说「开始训练」再来一轮。")
			}

		case agent.IntentUploadAbilityStandard:
			fmt.Println("\n检测到学习目标链接，开始准备自适应训练！")
			abilityStandardText, err := loader.ExtractAbilityStandardFromURL(ctx, input, chatModel)
			if err != nil {
				fmt.Printf("URL 抓取失败: %v\n请改用文件或手动输入方式，或说「开始训练」手动输入标准。\n", err)
				continue
			}
			fmt.Printf("[目标标准] 已从 URL 加载，长度: %d 字符\n", len(abilityStandardText))

			// 收集学生画像。
			studentProfileText := collectStudentProfile()
			if studentProfileText == "" {
				fmt.Println("学生画像为空，取消训练。继续聊天吧。")
				continue
			}
			fmt.Printf("[学生画像] 已加载，长度: %d 字符\n", len(studentProfileText))

			// 开始训练（复用上面的逻辑）。
			fmt.Println("\n" + strings.Repeat("=", 60))
			fmt.Println("学习状态分析与训练规划中，请稍候...")
			fmt.Println(strings.Repeat("=", 60))

			isTraining = true
			trainingScanner := bufio.NewScanner(os.Stdin)
			trainingScanner.Buffer(make([]byte, 1024*1024), 1024*1024)

			callbacks := &graph.TrainingCallbacks{
				OnStageChange: func(stage string, msg string) {
					fmt.Printf("\n[%s] %s\n", stage, msg)
				},
				OnQuestion: func(questionNum int, content string) {
					fmt.Printf("\n%s\n", strings.Repeat("-", 40))
					fmt.Printf("Student-Coach（第 %d 个训练任务）：\n\n%s\n\n", questionNum, content)
				},
				OnScore: func(score *agent.AnswerScore) {
					fmt.Printf("\n[评分: %.0f/100] %s\n", score.Score, score.Feedback)
				},
				OnReport: func(report string) {
					fmt.Println("\n" + strings.Repeat("=", 60))
					fmt.Println(report)
				},
				OnReviewPlan: func(plan string) {
					fmt.Println("\n" + strings.Repeat("=", 60))
					fmt.Println(plan)
				},
				GetUserAnswer: func() (string, error) {
					fmt.Print("你的回答（输入 END 结束本题，输入 quit 终止训练）：\n")
					answer := readMultiLine(trainingScanner)
					trimmed := strings.TrimSpace(strings.ToLower(answer))
					if trimmed == "quit" || trimmed == "exit" {
						return "", graph.ErrUserQuit
					}
					return answer, nil
				},
			}

			_, err = orchestrator.RunTraining(ctx, abilityStandardText, studentProfileText, "default_user", callbacks)
			if err != nil {
				fmt.Printf("\n训练流程出错: %v\n", err)
			}
			isTraining = false
			fmt.Println("\n本轮训练结束！你可以继续交流，或者说「开始训练」再来一轮。")

		case agent.IntentViewHistory:
			fmt.Println("\n[功能开发中] 历史训练与学习轨迹功能即将上线。")

		case agent.IntentChat:
			// 日常聊天
			resp, err := chatAgent.Chat(ctx, chatHistory, input)
			if err != nil {
				fmt.Printf("\n助手: 抱歉，处理出错了: %v\n", err)
				continue
			}
			// 更新对话历史
			chatHistory = append(chatHistory, schema.UserMessage(input), schema.AssistantMessage(resp, nil))
			fmt.Printf("\n助手: %s\n", resp)
		}
	}
}

// ============================================================
// runTrainingCLI 启动完整自适应学习训练流程；interview 仅作为历史命令名适配。
// ============================================================

func runTrainingCLI() {
	ctx := context.Background()

	// ====== 1. 加载配置 ======
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	fmt.Printf("模型: %s\n", cfg.LLM.Model)

	// ====== 1.5 初始化 MCP 网页抓取器（用于 URL 方式输入能力标准）======
	if err := loader.InitWebScraper(); err != nil {
		log.Printf("[MCP] Playwright 网页抓取器启动失败（URL 抓取不可用，可改用文件输入）: %v", err)
	} else {
		defer loader.CloseWebScraper()
		fmt.Println("[MCP] Playwright Web Scraper 就绪")
	}

	// ====== 2. 初始化 ChatModel ======
	chatModel, err := config.NewChatModel(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 ChatModel 失败: %v", err)
	}
	fmt.Println("[ChatModel] 就绪")

	// ====== 3. 连接 Redis ======
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("连接 Redis 失败 (%s): %v", cfg.Redis.Addr, err)
	}
	defer rdb.Close()
	fmt.Printf("[Redis] 已连接: %s\n", cfg.Redis.Addr)

	// ====== 4. 连接 MySQL ======
	mysqlStore, err := memory.NewMySQLStore(cfg.MySQL.DSN)
	if err != nil {
		log.Fatalf("连接 MySQL 失败: %v", err)
	}
	fmt.Println("[MySQL] 已连接，表结构就绪")

	// ====== 5. 组合存储 ======
	redisStore := memory.NewRedisStore(rdb)
	combinedStore := memory.NewCombinedStore(redisStore, mysqlStore)

	// ====== 6. Embedding + Milvus ======
	embedder, err := rag.NewEmbedder(ctx, cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.EmbeddingModel)
	if err != nil {
		log.Fatalf("创建 Embedder 失败: %v", err)
	}
	fmt.Printf("[Embedder] 就绪: %s\n", cfg.LLM.EmbeddingModel)

	milvusStore, err := rag.NewMilvusStore(ctx, cfg.Milvus.Addr, embedder, 10)
	if err != nil {
		log.Fatalf("连接 Milvus 失败 (%s): %v", cfg.Milvus.Addr, err)
	}
	defer milvusStore.Close()

	// ====== 7. BM25 ======
	bm25Manager := rag.NewBM25Manager(10)
	loadBuiltInStudentAbilityQuestions(bm25Manager)

	// ====== 8. GitHub MCP（可选） ======
	var trainingGitHubSearcher *mcp.GitHubSearcher
	if cfg.GitHub.Token != "" {
		gs, err := mcp.NewGitHubSearcher(cfg.GitHub.Token)
		if err != nil {
			log.Printf("[MCP] GitHub Searcher 启动失败: %v", err)
		} else {
			trainingGitHubSearcher = gs
			fmt.Println("[MCP] GitHub Searcher 就绪")
		}
	}

	// ====== 9. 编排器 ======
	orchestrator := graph.NewOrchestrator(&graph.OrchestratorConfig{
		ChatModel:      chatModel,
		Store:          combinedStore,
		MilvusStore:    milvusStore,
		BM25Manager:    bm25Manager,
		MySQLStore:     mysqlStore,
		GitHubSearcher: trainingGitHubSearcher,
		RerankerType:   cfg.LLM.RerankerType,
		RerankModel:    cfg.LLM.RerankModel,
		APIKey:         cfg.LLM.APIKey,
	})

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("所有基础设施就绪，可以开始自适应学习训练！")
	fmt.Println(strings.Repeat("=", 60))

	// ====== 10. 收集学习目标与能力标准 ======
	abilityStandardText := collectAbilityStandard(ctx, chatModel)
	if abilityStandardText == "" {
		fmt.Println("目标标准为空，退出")
		return
	}
	fmt.Printf("\n[目标标准] 已加载，长度: %d 字符\n", len(abilityStandardText))

	// ====== 10. 收集学生画像 ======
	studentProfileText := collectStudentProfile()
	if studentProfileText == "" {
		fmt.Println("学生画像为空，退出")
		return
	}
	fmt.Printf("[学生画像] 已加载，长度: %d 字符\n", len(studentProfileText))

	// ====== 11. 开始训练 ======
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("学习状态分析与训练规划中，请稍候...")
	fmt.Println(strings.Repeat("=", 60))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	callbacks := &graph.TrainingCallbacks{
		OnStageChange: func(stage string, msg string) {
			fmt.Printf("\n[%s] %s\n", stage, msg)
		},
		OnQuestion: func(questionNum int, content string) {
			fmt.Printf("\n%s\n", strings.Repeat("-", 40))
			fmt.Printf("Student-Coach（第 %d 个训练任务）：\n\n%s\n\n", questionNum, content)
		},
		OnScore: func(score *agent.AnswerScore) {
			fmt.Printf("\n[评分: %.0f/100] %s\n", score.Score, score.Feedback)
		},
		OnReport: func(report string) {
			fmt.Println("\n" + strings.Repeat("=", 60))
			fmt.Println(report)
		},
		OnReviewPlan: func(plan string) {
			fmt.Println("\n" + strings.Repeat("=", 60))
			fmt.Println(plan)
		},
		GetUserAnswer: func() (string, error) {
			fmt.Print("你的回答（输入 END 结束本题，输入 quit 终止训练）：\n")
			answer := readMultiLine(scanner)
			trimmed := strings.TrimSpace(strings.ToLower(answer))
			if trimmed == "quit" || trimmed == "exit" {
				return "", graph.ErrUserQuit
			}
			return answer, nil
		},
	}

	_, err = orchestrator.RunTraining(ctx, abilityStandardText, studentProfileText, "default_user", callbacks)
	if err != nil {
		log.Fatalf("训练流程出错: %v", err)
	}

	fmt.Println("\n感谢使用 Student-Coach，祝你稳步掌握每个知识点！")
}

// ============================================================
// 学习目标与能力标准输入：URL / 文件路径 / 手动输入。
// ============================================================

func collectAbilityStandard(ctx context.Context, chatModel model.ChatModel) string {
	fmt.Println()
	fmt.Println("请提供学习目标、课程标准、考试大纲或能力要求：")
	fmt.Println("  1. 粘贴相关页面 URL")
	fmt.Println("  2. 输入文件路径（如 ./teacher_standard.txt）")
	fmt.Println("  3. 直接粘贴标准文本（输入 END 结束）")
	fmt.Print("\n请输入: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		return ""
	}
	firstLine := strings.TrimSpace(scanner.Text())

	// 判断是 URL
	if loader.IsURL(firstLine) {
		fmt.Printf("检测到 URL，正在抓取: %s\n", firstLine)
		text, err := loader.ExtractAbilityStandardFromURL(ctx, firstLine, chatModel)
		if err != nil {
			fmt.Printf("URL 抓取失败: %v\n", err)
			fmt.Println("请改用文件或手动输入方式。")
			return ""
		}
		return text
	}

	// 判断是文件路径
	if loader.IsFilePath(firstLine) {
		fmt.Printf("检测到文件路径，正在读取: %s\n", firstLine)
		text, err := loader.LoadFile(firstLine)
		if err != nil {
			fmt.Printf("文件读取失败: %v\n", err)
			return ""
		}
		return text
	}

	// 其余当作手动输入的第一行，继续读取直到 END
	if firstLine == "END" {
		return ""
	}
	lines := []string{firstLine}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "END" {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ============================================================
// 学生画像输入：文件路径 / 手动输入。
// ============================================================

func collectStudentProfile() string {
	fmt.Println()
	fmt.Println("请提供学段、学科、学习经历、项目作品和能力基础等学生画像：")
	fmt.Println("  1. 输入文件路径（如 ./teacher_profile.pdf）")
	fmt.Println("  2. 直接粘贴学生画像文本（输入 END 结束）")
	fmt.Print("\n请输入: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		return ""
	}
	firstLine := strings.TrimSpace(scanner.Text())

	// 判断是文件路径
	if loader.IsFilePath(firstLine) {
		fmt.Printf("检测到文件路径，正在读取: %s\n", firstLine)
		text, err := loader.LoadFile(firstLine)
		if err != nil {
			fmt.Printf("文件读取失败: %v\n", err)
			return ""
		}
		return text
	}

	// 手动输入
	if firstLine == "END" {
		return ""
	}
	lines := []string{firstLine}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "END" {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ============================================================
// load-data 命令：加载知识点训练题库到 Milvus。
// ============================================================

func runLoadData() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
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

	// 如果指定了文件路径，加载单个文件（支持非 JSON 格式，走 LLM 解析）
	if len(os.Args) >= 3 {
		filePath := os.Args[2]
		ext := strings.ToLower(filepath.Ext(filePath))

		if ext == ".json" {
			// JSON 文件直接加载（CLI 模式用 default_user）
			fmt.Printf("正在加载 JSON 题库: %s ...\n", filePath)
			if err := milvusStore.LoadQuestionsFromFile(ctx, "default_user", filePath); err != nil {
				log.Fatalf("加载失败: %v", err)
			}
			fmt.Println("加载完成！")
		} else {
			// 非 JSON 文件：用 LLM 解析
			fmt.Printf("正在加载非结构化题库: %s ...\n", filePath)
			loadUnstructuredQuestions(ctx, cfg, milvusStore, filePath)
		}
		return
	}

	// 默认：扫描 data/questions/ 下所有 JSON 文件
	files, err := filepath.Glob("data/questions/*.json")
	if err != nil || len(files) == 0 {
		log.Fatalf("未找到题库文件，请确认 data/questions/ 目录下有 .json 文件")
	}

	totalDocs := 0
	for _, f := range files {
		fmt.Printf("正在加载: %s ...\n", f)
		if err := milvusStore.LoadQuestionsFromFile(ctx, "default_user", f); err != nil {
			log.Printf("  加载失败: %v", err)
			continue
		}
		fmt.Printf("  ✓ %s 加载完成\n", f)
		totalDocs++
	}

	fmt.Printf("\n题库加载完成！成功加载 %d 个文件\n", totalDocs)
}

// loadUnstructuredQuestions 加载非结构化题库文件（PDF/TXT/MD → LLM 解析 → Milvus）
func loadUnstructuredQuestions(ctx context.Context, cfg *config.Config, milvusStore *rag.MilvusStore, filePath string) {
	// 1. 提取文件文本
	rawText, err := loader.LoadFile(filePath)
	if err != nil {
		log.Fatalf("文件读取失败: %v", err)
	}
	fmt.Printf("文件内容已提取，长度: %d 字符\n", len(rawText))

	// 2. 初始化 ChatModel 用于 LLM 解析
	chatModel, err := config.NewChatModel(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 ChatModel 失败: %v", err)
	}

	// 3. LLM 解析
	fmt.Println("正在用 LLM 提取结构化题目...")
	result, err := loader.ParseQuestionBank(ctx, chatModel, rawText)
	if err != nil {
		log.Fatalf("LLM 解析失败: %v", err)
	}

	// 4. 输出解析结果
	fmt.Printf("\n题库解析完成：\n")
	fmt.Printf("  - 识别题目：%d 道\n", result.Total)
	fmt.Printf("  - 成功校验：%d 道\n", result.Success)
	fmt.Printf("  - 校验失败：%d 道\n", result.Failed)
	for _, e := range result.Errors {
		fmt.Printf("    #%d: %s\n", e.Index, e.Reason)
	}

	if result.Success == 0 {
		fmt.Println("\n无有效题目，退出。")
		return
	}

	// 5. 写入 Milvus
	fmt.Printf("\n正在写入 Milvus（%d 道题）...\n", result.Success)
	milvusQuestions := make([]struct {
		ID             string
		Subject        string
		Textbook       string
		Chapter        string
		KnowledgePoint string
		Content        string
		Reference      string
		Type           string
		Difficulty     string
		Skills         []string
	}, len(result.Questions))
	for i, q := range result.Questions {
		milvusQuestions[i] = struct {
			ID             string
			Subject        string
			Textbook       string
			Chapter        string
			KnowledgePoint string
			Content        string
			Reference      string
			Type           string
			Difficulty     string
			Skills         []string
		}{
			ID: q.ID, Subject: q.Subject, Textbook: q.Textbook, Chapter: q.Chapter,
			KnowledgePoint: q.KnowledgePoint, Content: q.Content, Reference: q.Reference,
			Type: q.Type, Difficulty: q.Difficulty, Skills: q.Skills,
		}
	}
	if err := milvusStore.LoadParsedQuestions(ctx, "default_user", "cli_import", milvusQuestions); err != nil {
		log.Fatalf("写入 Milvus 失败: %v", err)
	}

	fmt.Printf("题库导入完成！成功录入 %d 道题目\n", result.Success)
}

// ============================================================
// 工具函数
// ============================================================

func readMultiLine(scanner *bufio.Scanner) string {
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "END" {
			break
		}
		// 如果第一行就是 quit/exit，直接返回让调用方判断
		if len(lines) == 0 && (strings.ToLower(trimmed) == "quit" || strings.ToLower(trimmed) == "exit") {
			return trimmed
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ============================================================
// web 命令：启动 WebSocket HTTP 服务器
// ============================================================

func runWeb() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	fmt.Printf("模型: %s\n", cfg.LLM.Model)

	if err := loader.InitWebScraper(); err != nil {
		log.Printf("[MCP] Playwright 启动失败: %v", err)
	} else {
		defer loader.CloseWebScraper()
	}

	chatModel, err := config.NewChatModel(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 ChatModel 失败: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("连接 Redis 失败: %v", err)
	}
	defer rdb.Close()

	mysqlStore, err := memory.NewMySQLStore(cfg.MySQL.DSN)
	if err != nil {
		log.Fatalf("连接 MySQL 失败: %v", err)
	}

	redisStore := memory.NewRedisStore(rdb)
	combinedStore := memory.NewCombinedStore(redisStore, mysqlStore)

	embedder, err := rag.NewEmbedder(ctx, cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.EmbeddingModel)
	if err != nil {
		log.Fatalf("创建 Embedder 失败: %v", err)
	}

	milvusStore, err := rag.NewMilvusStore(ctx, cfg.Milvus.Addr, embedder, 10)
	if err != nil {
		log.Fatalf("连接 Milvus 失败: %v", err)
	}
	defer milvusStore.Close()

	bm25Manager := rag.NewBM25Manager(10)
	loadBuiltInStudentAbilityQuestions(bm25Manager)

	// 初始化 GitHub MCP（可选）
	var webGitHubSearcher *mcp.GitHubSearcher
	if cfg.GitHub.Token != "" {
		gs, err := mcp.NewGitHubSearcher(cfg.GitHub.Token)
		if err != nil {
			log.Printf("[MCP] GitHub Searcher 启动失败: %v", err)
		} else {
			webGitHubSearcher = gs
			fmt.Println("[MCP] GitHub Searcher 就绪")
		}
	}

	// 初始化认证服务
	authService, err := auth.NewService(mysqlStore.GetDB(), cfg.JWT.Secret)
	if err != nil {
		log.Fatalf("创建认证服务失败: %v", err)
	}

	chatAgent := agent.NewChatAgent(chatModel)
	router := agent.NewIntentRouter()

	// 初始化 Skill 注册中心
	skillRegistry := skill.NewSkillRegistry()
	skillRegistry.Register(skill.NewConceptTutorSkill(chatModel, milvusStore, bm25Manager))
	skillRegistry.Register(skill.NewQuickQuizSkill(chatModel, milvusStore, bm25Manager))
	skillRegistry.Register(skill.NewErrorReviewSkill(chatModel, milvusStore, bm25Manager))
	skillRegistry.Register(skill.NewGuidedPracticeSkill(chatModel, milvusStore, bm25Manager))
	skillRegistry.Register(skill.NewKnowledgeCompareSkill(chatModel, milvusStore, bm25Manager))

	var ttsProvider speech.Synthesizer
	if cfg.Speech.Enabled && cfg.Speech.TTSEnabled {
		ttsClient, err := dashscope.NewClient(dashscope.ClientConfig{
			APIKey:            cfg.Speech.APIKey,
			BaseURL:           cfg.Speech.TTSBaseURL,
			Model:             cfg.Speech.TTSModel,
			Voice:             cfg.Speech.TTSVoice,
			Language:          cfg.Speech.TTSLanguage,
			Timeout:           cfg.Speech.TTSTimeout,
			MaxAudioBytes:     cfg.Speech.MaxTTSAudioBytes,
			AllowedAudioHosts: dashscope.DefaultAllowedAudioHosts(),
		})
		if err != nil {
			log.Fatalf("创建 DashScope TTS 客户端失败: %v", err)
		}
		ttsProvider = ttsClient
	}
	var asrProvider speech.Transcriber
	if cfg.Speech.Enabled && cfg.Speech.ASREnabled {
		realtimeASRClient, err := dashscope.NewRealtimeASRClient(dashscope.RealtimeASRConfig{
			APIKey:         cfg.Speech.APIKey,
			URL:            cfg.Speech.ASRRealtimeURL,
			Model:          cfg.Speech.ASRRealtimeModel,
			ConnectTimeout: cfg.Speech.ASRConnectTimeout,
			WriteTimeout:   cfg.Speech.ASRConnectTimeout,
			QueueSize:      32,
		})
		if err != nil {
			log.Fatalf("创建 DashScope 实时 ASR 客户端失败: %v", err)
		}
		fallbackASRClient, err := dashscope.NewFallbackASRClient(dashscope.FallbackASRConfig{
			APIKey:  cfg.Speech.APIKey,
			BaseURL: cfg.Speech.ASRFallbackBaseURL,
			Model:   cfg.Speech.ASRFallbackModel,
			Timeout: cfg.Speech.ASRFallbackTimeout,
		})
		if err != nil {
			log.Fatalf("创建 DashScope HTTP ASR 客户端失败: %v", err)
		}
		asrClient, err := dashscope.NewASRClient(realtimeASRClient, fallbackASRClient)
		if err != nil {
			log.Fatalf("创建 DashScope ASR 客户端失败: %v", err)
		}
		asrProvider = asrClient
	}

	speechService, err := speech.NewService(speech.ServiceConfig{
		Enabled:            cfg.Speech.Enabled,
		TTSEnabled:         cfg.Speech.TTSEnabled,
		ASREnabled:         cfg.Speech.ASREnabled,
		MaxAnswerSeconds:   cfg.Speech.MaxAnswerSeconds,
		MaxTTSChars:        cfg.Speech.MaxTTSChars,
		TTSTimeout:         cfg.Speech.TTSTimeout,
		ASRConnectTimeout:  cfg.Speech.ASRConnectTimeout,
		ASRFinalTimeout:    cfg.Speech.ASRFinalTimeout,
		ASRFallbackTimeout: cfg.Speech.ASRFallbackTimeout,
		TTSConcurrency:     cfg.Speech.TTSConcurrency,
		ASRConcurrency:     cfg.Speech.ASRConcurrency,
	}, ttsProvider, asrProvider)
	if err != nil {
		log.Fatalf("创建语音服务失败: %v", err)
	}

	server := handler.NewServer(&handler.ServerConfig{
		ChatModel:              chatModel,
		CombinedStore:          combinedStore,
		MilvusStore:            milvusStore,
		BM25Manager:            bm25Manager,
		RedisStore:             redisStore,
		MySQLStore:             mysqlStore,
		ChatAgent:              chatAgent,
		Router:                 router,
		SkillRegistry:          skillRegistry,
		GitHubSearcher:         webGitHubSearcher,
		AuthService:            authService,
		RerankerType:           cfg.LLM.RerankerType,
		RerankModel:            cfg.LLM.RerankModel,
		APIKey:                 cfg.LLM.APIKey,
		SpeechService:          speechService,
		SpeechLogger:           speech.NewJSONEventLogger(os.Stdout, cfg.JWT.Secret),
		SpeechProvider:         "dashscope",
		SpeechTTSModel:         cfg.Speech.TTSModel,
		SpeechASRRealtimeModel: cfg.Speech.ASRRealtimeModel,
		SpeechASRFallbackModel: cfg.Speech.ASRFallbackModel,
		AllowedOrigins:         cfg.Speech.AllowedOrigins,
	})

	addr := ":9090"
	fmt.Printf("\n[Web] 服务器启动: http://localhost%s\n", addr)
	fmt.Println("[Web] 前端请访问: http://localhost:5173")
	log.Fatal(server.Start(addr))
}

func printUsage() {
	fmt.Println("用法: student-coach <command>")
	fmt.Println()
	fmt.Println("可用命令:")
	fmt.Println("  chat              进入 Student-Coach 学习答疑模式")
	fmt.Println("  train             启动自适应学习训练与知识掌握度诊断")
	fmt.Println("  interview         train 的历史兼容别名")
	fmt.Println("  load-data         （可选）加载内置知识点训练题库到 Milvus")
	fmt.Println("  load-data <file>  加载知识点训练资料（支持 PDF/TXT/MD，自动 LLM 解析）")
	fmt.Println("  web               启动 Web 服务器（前端 + WebSocket API）")
	fmt.Println("  eval [flags]      跑 RAG 离线评估（详见 go run cmd/main.go eval -h）")
	fmt.Println()
	fmt.Println("学习目标输入方式（train 命令中）：")
	fmt.Println("  - URL:   粘贴课程标准、考试大纲或能力要求页面")
	fmt.Println("  - 文件:  支持 .txt / .pdf / .docx / .md 格式")
	fmt.Println("  - 手动:  直接粘贴文本，输入 END 结束")
	fmt.Println()
	fmt.Println("学生画像输入方式：")
	fmt.Println("  - 文件:  支持 .pdf / .docx / .txt 格式")
	fmt.Println("  - 手动:  直接粘贴文本，输入 END 结束")
	fmt.Println()
	fmt.Println("首次运行步骤:")
	fmt.Println("  1. cp .env.example .env && 填入 DASHSCOPE_API_KEY")
	fmt.Println("  2. make infra-up                    # 启动 Milvus/Redis/MySQL")
	fmt.Println("  3. go run cmd/main.go               # 进入 AI 教研聊天模式")
	fmt.Println("  4. go run cmd/main.go train         # 直接开始自适应学习训练")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  go run cmd/main.go train")
	fmt.Println("  > 请输入: https://example.edu/learning-standard.html")
	fmt.Println("  > 请输入: ./student_profile.pdf")
}

// loadBuiltInStudentAbilityQuestions 让系统在未连接或尚未导入向量库时，也能使用内置学生能力题库。
func loadBuiltInStudentAbilityQuestions(manager *rag.BM25Manager) {
	const filePath = "data/questions/student_ability_training.json"
	docs, err := rag.LoadBM25DocsFromFile(filePath)
	if err != nil {
		log.Printf("[StudentAbilityBank] 内置学生能力题库加载失败: %v", err)
		return
	}
	schemaDocs := make([]*schema.Document, len(docs))
	for i, doc := range docs {
		schemaDocs[i] = &schema.Document{
			ID:      doc.ID,
			Content: doc.Content + "\n参考答案：" + doc.Reference,
		}
	}
	manager.ReplaceDocuments("default_user", schemaDocs)
	log.Printf("[StudentAbilityBank] 已加载 %d 道内置知识点训练题", len(schemaDocs))
}
