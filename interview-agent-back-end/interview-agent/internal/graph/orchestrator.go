/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

// Package graph 用 Eino compose.Graph 编排学生能力训练全流程。
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"interview-agent/internal/agent"
	"interview-agent/internal/mcp"
	"interview-agent/internal/memory"
	imodel "interview-agent/internal/model"
	"interview-agent/internal/rag"
	growthservice "interview-agent/internal/service"
	educationtool "interview-agent/internal/tool"
)

// ErrUserQuit 用户主动终止面试
var ErrUserQuit = errors.New("用户主动终止面试")

// Orchestrator 学生能力训练流程编排器，管理各 Agent 的协作。
type Orchestrator struct {
	abilityAnalyzer        *agent.AbilityAnalyzer
	studentProfileAnalyzer *agent.StudentProfileAnalyzer
	questionPlanner        *agent.QuestionPlanner
	studentCoach           *agent.StudentCoach
	abilityEvaluator       *agent.AbilityEvaluator
	growthPlanner          *agent.GrowthPlanner

	// 记忆系统
	shortTermMem *memory.ShortTermMemory
	longTermMem  *memory.LongTermMemory

	// RAG 多路召回
	milvusStore *rag.MilvusStore   // Milvus 向量存储（支持按用户过滤）
	bm25Manager *rag.BM25Manager   // BM25 按用户管理
	reranker    rag.RerankStrategy // 重排策略（LLM / cross-encoder / none，可切换）

	// 持久化
	mysqlStore *memory.MySQLStore // MySQL（保存训练记录）

	// 学生成长业务服务（聚合能力画像并保存成长记录）
	growthService *growthservice.StudentGrowthDataService

	// 项目内部业务 Trace（不改变 Graph 节点与边）。
	traceService *growthservice.AgentTraceService
}

// OrchestratorConfig 编排器配置
type OrchestratorConfig struct {
	ChatModel      model.ChatModel
	Store          memory.Store        // Redis+MySQL 组合存储
	MilvusStore    *rag.MilvusStore    // Milvus 向量存储（支持按用户检索/删除）
	BM25Manager    *rag.BM25Manager    // BM25 按用户管理
	MySQLStore     *memory.MySQLStore  // MySQL 直接引用（保存训练记录）
	GitHubSearcher *mcp.GitHubSearcher // GitHub MCP 搜索器（可选，用于复习计划推荐）
	RerankerType   string              // 重排策略：cross-encoder（默认）/ llm / none
	RerankModel    string              // cross-encoder 重排模型名（默认 gte-rerank-v2）
	APIKey         string              // DashScope API Key（cross-encoder rerank 调用用）
	TraceService   *growthservice.AgentTraceService
}

// newReranker 按配置选择重排策略，默认 cross-encoder（仅显式 llm / none 才切换）。
// 优先用 OrchestratorConfig 显式传入的值，未设置时回退环境变量（RERANKER_TYPE / RERANK_MODEL / DASHSCOPE_API_KEY）。
func newReranker(cfg *OrchestratorConfig) rag.RerankStrategy {
	rerankerType := cfg.RerankerType
	if rerankerType == "" {
		rerankerType = os.Getenv("RERANKER_TYPE")
	}
	switch rerankerType {
	case "llm":
		return rag.NewReranker(cfg.ChatModel, 10)
	case "none":
		return rag.NewNoneReranker(10)
	default: // cross-encoder（含空值/未知值，默认策略）
		apiKey := cfg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("DASHSCOPE_API_KEY")
		}
		model := cfg.RerankModel
		if model == "" {
			model = os.Getenv("RERANK_MODEL")
		}
		return rag.NewCrossEncoderReranker(apiKey, model, 10)
	}
}

// NewOrchestrator 创建编排器
func NewOrchestrator(cfg *OrchestratorConfig) *Orchestrator {
	traceService := cfg.TraceService
	if traceService == nil {
		traceService = growthservice.NewAgentTraceService(cfg.Store)
	}
	o := &Orchestrator{
		abilityAnalyzer:        agent.NewAbilityAnalyzer(cfg.ChatModel),
		studentProfileAnalyzer: agent.NewStudentProfileAnalyzer(cfg.ChatModel),
		questionPlanner:        agent.NewQuestionPlanner(cfg.ChatModel),
		studentCoach:           agent.NewStudentCoach(cfg.ChatModel),
		abilityEvaluator:       agent.NewAbilityEvaluator(cfg.ChatModel),
		growthPlanner:          agent.NewGrowthPlanner(cfg.ChatModel),
		shortTermMem:           memory.NewShortTermMemory(20),
		longTermMem:            memory.NewLongTermMemory(cfg.Store),
		milvusStore:            cfg.MilvusStore,
		bm25Manager:            cfg.BM25Manager,
		reranker:               newReranker(cfg),
		mysqlStore:             cfg.MySQLStore,
		growthService:          growthservice.NewStudentGrowthDataService(cfg.Store, cfg.MySQLStore, cfg.MilvusStore, cfg.BM25Manager),
		traceService:           traceService,
	}

	// 设置 GitHub MCP 搜索器（可选）
	if cfg.GitHubSearcher != nil {
		o.growthPlanner.SetGitHubSearcher(cfg.GitHubSearcher)
	}

	return o
}

// TrainingCallbacks 训练过程回调，用于 CLI/Web 等不同界面。
type TrainingCallbacks struct {
	OnStageChange func(stage string, msg string)
	OnQuestion    func(questionNum int, content string)
	OnScore       func(score *agent.AnswerScore)
	OnReport      func(report string)
	OnReviewPlan  func(plan string)
	GetUserAnswer func() (string, error)
}

// trainingCtx 单次能力训练的上下文持有者。
// 注意：不放进 graph 的数据流，而是由各节点闭包捕获共享——graph 只负责编排节点的执行顺序与分支。
type trainingCtx struct {
	abilityStandardText string
	studentProfileText  string
	userID              string
	cb                  *TrainingCallbacks

	session           *imodel.Session
	abilityStandard   *imodel.AbilityStandard
	studentProfile    *imodel.StudentProfile
	learningDiagnosis *imodel.LearningDiagnosis
	plan              *imodel.QuestionPlan
	trainingState     *imodel.TrainingState
	report            *imodel.EvaluationReport
	abilityProfile    *imodel.StudentAbilityProfile
	abilityBefore     map[string]float64
	traceSkillBound   bool
	userTerminated    bool
}

// RunTraining 执行完整能力训练流程。Graph 节点与边保持历史结构不变。
func (o *Orchestrator) RunTraining(ctx context.Context, abilityStandardText string, studentProfileText string, userID string, cb *TrainingCallbacks) (*imodel.Session, error) {
	sessionID := uuid.New().String()
	ic := &trainingCtx{abilityStandardText: abilityStandardText, studentProfileText: studentProfileText, userID: userID, cb: cb}
	ic.session = &imodel.Session{ID: sessionID, UserID: userID, StudentID: userID, Status: imodel.StatusInit, CreatedAt: time.Now()}
	if o.traceService != nil {
		if _, err := o.traceService.Create(ctx, sessionID, userID, traceIntentForGoal(abilityStandardText)); err != nil {
			log.Printf("[AgentTrace] 创建失败（不影响训练主流程）: %v", err)
		} else {
			ctx = educationtool.WithTrace(ctx, sessionID, o.traceService)
		}
	}

	g := compose.NewGraph[string, string]()

	_ = g.AddLambdaNode("ability_analysis", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeAbilityAnalysis(ctx, ic)
	}))
	_ = g.AddLambdaNode("student_profile_analysis", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeStudentProfileAnalysis(ctx, ic)
	}))
	_ = g.AddLambdaNode("question_plan", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeQuestionPlan(ctx, ic)
	}))
	_ = g.AddLambdaNode("training", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeTraining(ctx, ic)
	}))
	_ = g.AddLambdaNode("weak_review", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeWeakReview(ctx, ic)
	}))
	_ = g.AddLambdaNode("evaluation", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeEvaluation(ctx, ic)
	}))
	_ = g.AddLambdaNode("growth_plan", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeGrowthPlan(ctx, ic)
	}))

	_ = g.AddEdge(compose.START, "ability_analysis")
	_ = g.AddEdge("ability_analysis", "student_profile_analysis")
	_ = g.AddEdge("student_profile_analysis", "question_plan")
	_ = g.AddEdge("question_plan", "training")
	// training 之后的条件分支：用户未作答即终止 → END；否则 → weak_review
	_ = g.AddBranch("training", compose.NewGraphBranch(
		func(ctx context.Context, _ string) (string, error) { return o.afterTraining(ic), nil },
		map[string]bool{"weak_review": true, compose.END: true}))
	_ = g.AddEdge("weak_review", "evaluation")
	_ = g.AddEdge("evaluation", "growth_plan")
	_ = g.AddEdge("growth_plan", compose.END)

	runnable, err := g.Compile(ctx)
	if err != nil {
		o.markTraceStatus(ctx, sessionID, imodel.AgentTraceStatusFailed)
		return nil, fmt.Errorf("orchestrator: compile graph: %w", err)
	}
	log.Printf("[Orchestrator] 学生能力训练 compose.Graph 已编译，开始执行")

	if _, err := runnable.Invoke(ctx, ""); err != nil {
		o.markTraceStatus(ctx, sessionID, imodel.AgentTraceStatusFailed)
		return nil, err
	}
	if ic.userTerminated {
		o.markTraceStatus(ctx, sessionID, imodel.AgentTraceStatusTerminated)
	}

	return ic.session, nil
}

// ============================================================
// 各阶段节点（读写 trainingCtx，调回调推送前端；返回 error 会中断 graph）
// ============================================================

// nodeAbilityAnalysis 阶段 1：能力标准分析。
func (o *Orchestrator) nodeAbilityAnalysis(ctx context.Context, ic *trainingCtx) error {
	ic.cb.OnStageChange("ability_analysis", "正在解析学习目标与能力标准...")

	standard, err := o.abilityAnalyzer.Analyze(ctx, ic.abilityStandardText)
	if err != nil {
		return fmt.Errorf("orchestrator: ability analysis: %w", err)
	}
	ic.abilityStandard = standard
	ic.session.AbilityStandard = standard
	ic.session.Status = imodel.StatusAbilityAnalyzed
	if o.traceService != nil {
		traceGoal := standard.LearningGoal
		if imodel.NormalizeAbilityDimension(traceGoal) == "" {
			traceGoal = ic.abilityStandardText
		}
		_ = o.traceService.UpdateIntent(ctx, ic.session.ID, traceIntentForGoal(traceGoal))
		if skillName, reason := traceSkillDecision(traceGoal); skillName != "" {
			_ = o.traceService.UpdateSkill(ctx, ic.session.ID, skillName, reason)
		}
	}

	ic.cb.OnStageChange("ability_analysis_done", fmt.Sprintf("能力标准解析完成：%s", standard.LearningGoal))
	return nil
}

// nodeStudentProfileAnalysis 阶段 2：学生画像分析。
func (o *Orchestrator) nodeStudentProfileAnalysis(ctx context.Context, ic *trainingCtx) error {
	ic.cb.OnStageChange("student_profile_analysis", "正在建立学生能力训练起点...")

	ic.studentProfile = &imodel.StudentProfile{
		StudentID:    ic.userID,
		Grade:        ic.abilityStandard.Grade,
		Subject:      ic.abilityStandard.Subject,
		LearningGoal: ic.abilityStandard.LearningGoal,
		RawText:      ic.studentProfileText,
	}
	ic.session.StudentProfile = ic.studentProfile
	if o.traceService != nil {
		_ = o.traceService.RecordMemorySummary(ctx, ic.session.ID, "student_profile=available")
	}

	diagnosis, err := o.studentProfileAnalyzer.Analyze(ctx, ic.abilityStandard, ic.studentProfile)
	if err != nil {
		return fmt.Errorf("orchestrator: student profile analysis: %w", err)
	}
	ic.learningDiagnosis = diagnosis
	ic.session.LearningDiagnosis = diagnosis
	ic.session.Status = imodel.StatusStudentProfileAnalyzed

	ic.cb.OnStageChange("student_profile_analysis_done", fmt.Sprintf("学生画像诊断完成，当前能力证据覆盖度：%.0f%%", diagnosis.OverallScore))
	return nil
}

// nodeQuestionPlan 阶段 2.5 + 3：读取历史薄弱点 + 出题规划（Phase1 方向 + Phase2 检索/组装）
func (o *Orchestrator) nodeQuestionPlan(ctx context.Context, ic *trainingCtx) error {
	standard := ic.abilityStandard
	diagnosis := ic.learningDiagnosis
	userID := ic.userID

	// ===== 阶段 2.5：读取历史薄弱点（长期记忆），并按当前能力标准过滤 =====
	var weakPointsContext string
	var memoryLines []string
	abilityProfile, profileErr := o.growthService.GetAbilityProfile(ctx, userID)
	if profileErr != nil {
		log.Printf("[Profile] 读取长期能力画像失败（继续使用本轮画像）: %v", profileErr)
	} else {
		ic.abilityProfile = abilityProfile
		ic.abilityBefore = copyTraceScores(abilityProfile.AbilityScores)
		if o.traceService != nil {
			summaries := traceAbilitySummaries(abilityProfile.AbilityScores)
			if len(summaries) == 0 {
				summaries = append(summaries, "ability_profile=empty")
			}
			_ = o.traceService.RecordMemorySummary(ctx, ic.session.ID, summaries...)
		}
		for _, ability := range imodel.CoreAbilityDimensions() {
			if score, ok := abilityProfile.AbilityScores[ability]; ok {
				memoryLines = append(memoryLines, fmt.Sprintf("- %s：长期能力分 %.0f%%", ability, score*100))
			}
		}
	}
	weakPoints := o.longTermMem.GetWeakPoints(ctx, userID)
	if o.traceService != nil {
		_ = o.traceService.RecordMemorySummary(ctx, ic.session.ID, fmt.Sprintf("growth_history_weaknesses=%d", len(weakPoints)))
	}
	if len(weakPoints) > 0 {
		standardAbilities := collectStandardAbilities(standard)
		for _, wp := range weakPoints {
			if isWeakPointRelevant(wp.Topic, standardAbilities) {
				memoryLines = append(memoryLines, fmt.Sprintf("- %s：历史得分 %.0f，被考察 %d 次，答错 %d 次",
					wp.Topic, wp.Score, wp.HitCount, wp.WrongCount))
			}
		}
	}
	if len(memoryLines) > 0 {
		weakPointsContext = strings.Join(memoryLines, "\n")
		ic.cb.OnStageChange("memory_loaded", fmt.Sprintf("已加载 %d 条长期能力画像与历史训练证据，将针对性练习", len(memoryLines)))
	}

	// ===== 阶段 3 Phase 1：规划出题方向 =====
	ic.cb.OnStageChange("question_plan", "正在规划个性化学生能力训练方向...")

	dirPlan, err := o.questionPlanner.PlanDirections(ctx, standard, diagnosis, weakPointsContext)
	if err != nil {
		return fmt.Errorf("orchestrator: plan directions: %w", err)
	}
	log.Printf("[Plan] Phase 1 完成，规划了 %d 个出题方向", len(dirPlan.Directions))

	// ===== 阶段 3 Phase 2：按方向检索题库 + 组装题目 =====
	hasRAG := o.milvusStore != nil || o.bm25Manager != nil
	var matchedQuestions []imodel.PlannedQuestion // 题库匹配到的题目（代码直接构建）
	var unmatchedDirs []imodel.QuestionDirection  // 未匹配的方向（交给 LLM）
	matchedCount := 0

	if hasRAG {
		ic.cb.OnStageChange("rag_retrieval", "正在从训练题库检索基础理解题目...")

		for i, dir := range dirPlan.Directions {
			// 实践与综合情境需要结合学生画像动态生成；基础理解题可优先检索题库。
			if imodel.NormalizeQuestionType(dir.Type) != imodel.QuestionTypeTheory {
				unmatchedDirs = append(unmatchedDirs, dir)
				continue
			}
			query := dir.SearchQuery
			if query == "" {
				query = dir.Topic
			}
			log.Printf("[RAG] 方向 %d 检索: query=%s", i+1, query)

			var docs []*schema.Document
			seen := make(map[string]bool)

			if o.milvusStore != nil {
				milvusDocs, mErr := o.milvusStore.RetrieveByUser(ctx, userID, query)
				if mErr != nil {
					log.Printf("[RAG] Milvus 检索失败（方向%d）: %v", i+1, mErr)
				} else {
					for _, doc := range milvusDocs {
						if !seen[doc.ID] {
							seen[doc.ID] = true
							docs = append(docs, doc)
						}
					}
				}
			}
			if o.bm25Manager != nil {
				bm25Docs, bErr := o.bm25Manager.Retrieve(ctx, userID, query)
				if bErr != nil {
					log.Printf("[RAG] BM25 检索失败（方向%d）: %v", i+1, bErr)
				} else {
					for _, doc := range bm25Docs {
						if !seen[doc.ID] {
							seen[doc.ID] = true
							docs = append(docs, doc)
						}
					}
				}
			}

			// 用户私有题库没有命中时，再回退到系统内置题库；私有数据仍按 userID 隔离。
			if len(docs) == 0 && userID != "default_user" {
				if o.milvusStore != nil {
					sharedDocs, sErr := o.milvusStore.RetrieveByUser(ctx, "default_user", query)
					if sErr == nil {
						for _, doc := range sharedDocs {
							if !seen[doc.ID] {
								seen[doc.ID] = true
								docs = append(docs, doc)
							}
						}
					}
				}
				if o.bm25Manager != nil {
					sharedDocs, sErr := o.bm25Manager.Retrieve(ctx, "default_user", query)
					if sErr == nil {
						for _, doc := range sharedDocs {
							if !seen[doc.ID] {
								seen[doc.ID] = true
								docs = append(docs, doc)
							}
						}
					}
				}
			}

			if len(docs) > 0 {
				// Rerank 取 top 1
				reranked, rErr := o.reranker.Rerank(ctx, query, docs)
				if rErr != nil || len(reranked) == 0 {
					reranked = docs
				}
				topDoc := reranked[0]
				log.Printf("[RAG] 方向 %d 匹配到题库原题 [%s]: %s", i+1, topDoc.ID, topDoc.Content)

				// 直接用题库原题构建题目，不经过 LLM
				questionContent := topDoc.Content
				reference := ""
				if idx := strings.Index(questionContent, "\n参考答案："); idx >= 0 {
					reference = strings.TrimSpace(questionContent[idx+len("\n参考答案："):])
					questionContent = strings.TrimSpace(questionContent[:idx])
				} else if idx := strings.Index(questionContent, "\n参考答案:"); idx >= 0 {
					reference = strings.TrimSpace(questionContent[idx+len("\n参考答案:"):])
					questionContent = strings.TrimSpace(questionContent[:idx])
				}

				matchedQuestions = append(matchedQuestions, imodel.PlannedQuestion{
					ID:         fmt.Sprintf("q%d", i+1),
					Content:    questionContent,
					Type:       dir.Type,
					Difficulty: dir.Difficulty,
					Skills:     dir.Skills,
					FollowUps:  []string{},
					Reference:  reference,
					Source:     topDoc.ID,
				})
				matchedCount++
			} else {
				log.Printf("[RAG] 方向 %d 无匹配题目，交给 LLM", i+1)
				unmatchedDirs = append(unmatchedDirs, dir)
			}
		}

		ic.cb.OnStageChange("rag_retrieval_done", fmt.Sprintf("题库检索完成，%d 道用原题，%d 道由 LLM 出题", matchedCount, len(unmatchedDirs)))
	} else {
		unmatchedDirs = dirPlan.Directions
	}

	// 未匹配的方向交给 LLM 出题
	var llmQuestions []imodel.PlannedQuestion
	if len(unmatchedDirs) > 0 {
		ic.cb.OnStageChange("question_assemble", fmt.Sprintf("正在为 %d 个方向生成能力训练题目...", len(unmatchedDirs)))
		unmatchedPlan := &imodel.QuestionDirectionPlan{Directions: unmatchedDirs}
		emptyDocs := make([]string, len(unmatchedDirs)) // 全部无匹配
		assembled, aErr := o.questionPlanner.AssembleQuestions(ctx, standard, diagnosis, unmatchedPlan, emptyDocs)
		if aErr != nil {
			return fmt.Errorf("orchestrator: assemble questions: %w", aErr)
		}
		llmQuestions = assembled.Questions
	}

	// 合并题目：题库原题 + LLM 出题
	allQuestions := append(matchedQuestions, llmQuestions...)
	for i := range allQuestions {
		allQuestions[i].ID = fmt.Sprintf("q%d", i+1)
		allQuestions[i].Type = imodel.NormalizeQuestionType(allQuestions[i].Type)
	}

	// 统计分布
	var basicCount, expCount, designCount int
	for _, q := range allQuestions {
		switch q.Type {
		case imodel.QuestionTypeTheory:
			basicCount++
		case imodel.QuestionTypePractice:
			expCount++
		case imodel.QuestionTypeScenario:
			designCount++
		}
	}

	plan := &imodel.QuestionPlan{
		TotalQuestions: len(allQuestions),
		Distribution:   imodel.QuestionDistrib{Theory: basicCount, Practice: expCount, Scenario: designCount},
		Questions:      allQuestions,
	}
	ic.plan = plan
	ic.session.QuestionPlan = plan
	ic.session.Status = imodel.StatusPlanned

	ic.cb.OnStageChange("question_plan_done", fmt.Sprintf("训练题池完成，共 %d 道题（基础理解%d/实践应用%d/综合情境%d）",
		plan.TotalQuestions, basicCount, expCount, designCount))
	return nil
}

// nodeTraining 阶段 4：能力训练（含追问、动态难度调节、薄弱点更新，人在环阻塞交互）。
func (o *Orchestrator) nodeTraining(ctx context.Context, ic *trainingCtx) error {
	standard := ic.abilityStandard
	plan := ic.plan
	userID := ic.userID

	ic.cb.OnStageChange("training", "学生能力训练正式开始！")

	// 训练分三个阶段顺序进行：基础理解 → 实践应用 → 综合情境。
	// 阶段化取题与阶段内难度调节由 stageScheduler 负责（见 stage_scheduler.go）：
	// 每阶段从候选池按当前难度自适应抽取固定道数；进入新阶段时难度重置为 medium，不继承上一阶段。
	if ic.abilityProfile == nil {
		ic.abilityProfile = &imodel.StudentAbilityProfile{
			StudentID:     ic.studentProfile.StudentID,
			AbilityScores: map[string]float64{},
		}
	}
	initialDifficulty := growthservice.RecommendStartingDifficulty(ic.abilityProfile)
	sched := newStageScheduler(defaultStages, plan.Questions,
		func(cur imodel.DifficultyLevel, consecRight, consecWrong int) imodel.DifficultyLevel {
			return o.questionPlanner.AdjustDifficulty(&imodel.TrainingState{
				CurrentDifficulty: cur,
				ConsecutiveRight:  consecRight,
				ConsecutiveWrong:  consecWrong,
			})
		}, initialDifficulty)

	state := &imodel.TrainingState{
		SessionID:             ic.session.ID,
		TotalQuestions:        sched.totalToAsk(),
		CurrentDifficulty:     initialDifficulty,
		StudentAbilityProfile: ic.abilityProfile,
	}
	ic.trainingState = state
	ic.session.TrainingState = state
	ic.session.Status = imodel.StatusTraining

	userTerminated := false
	asked := 0
	for {
		q, difficulty, done := sched.next()
		if done {
			break
		}
		asked++
		state.CurrentQuestion = asked
		state.CurrentDifficulty = difficulty
		log.Printf("[难度调节] 第%d题 type=%s 抽取难度=%s (上一题后 连对%d/连错%d) 来源=%s",
			asked, q.Type, difficulty, sched.consecRight, sched.consecWrong, q.Source)

		// 学习教练提问
		questionText, err := o.studentCoach.AskQuestion(ctx, state, &q, standard.LearningGoal)
		if err != nil {
			return fmt.Errorf("orchestrator: ask question %d: %w", asked, err)
		}
		questionText = strings.TrimSpace(questionText)
		attempt := newTrainingAttempt(userID, q, questionText, q.Reference, imodel.TrainingAttemptTypePrimary, "")
		state.TrainingAttempts = append(state.TrainingAttempts, attempt)
		if o.traceService != nil {
			if !ic.traceSkillBound {
				_ = o.traceService.UpdateSkill(ctx, ic.session.ID, attempt.SkillName, traceAttemptDecisionReason(attempt))
				ic.traceSkillBound = true
			}
			_ = o.traceService.BindTrainingAttempt(ctx, ic.session.ID, attempt.ID)
		}

		// 标注题目来源
		displayQuestion := questionText
		if q.Source != "" && q.Source != "llm" {
			displayQuestion += fmt.Sprintf("\n\n`[来源: 题库 %s]`", q.Source)
		} else {
			displayQuestion += "\n\n`[来源: LLM 出题]`"
		}

		// 发送题目到前端
		ic.cb.OnQuestion(asked, displayQuestion)

		// 等待用户回答
		answer, err := ic.cb.GetUserAnswer()
		if err != nil {
			if errors.Is(err, ErrUserQuit) {
				userTerminated = true
				ic.cb.OnStageChange("terminated", fmt.Sprintf("学员主动终止训练（已完成 %d/%d 题）", len(state.QAHistory), state.TotalQuestions))
				break
			}
			return fmt.Errorf("orchestrator: get answer: %w", err)
		}
		attempt.RecordAnswer(answer)

		// 评分
		score, err := o.studentCoach.ScoreTrainingAttempt(ctx, attempt)
		if err != nil {
			return fmt.Errorf("orchestrator: score answer %d: %w", asked, err)
		}
		ic.cb.OnScore(score)

		// 更新学生能力画像
		updatedProfile, profileErr := o.studentCoach.UpdateStudentAbilityProfileFromAttempt(ctx, state.StudentAbilityProfile, asked, attempt)
		if profileErr != nil {
			log.Printf("[Profile] 画像更新失败（不影响主流程）: %v", profileErr)
		} else {
			state.StudentAbilityProfile = updatedProfile
		}

		// 记录问答
		askedQuestion := q
		askedQuestion.Content = attempt.Question
		askedQuestion.Reference = attempt.ReferenceAnswer
		qa := imodel.QAPair{
			AttemptID:  attempt.ID,
			Question:   askedQuestion,
			UserAnswer: answer,
			Score:      score.Score,
			Feedback:   score.Feedback,
		}

		// 追问逻辑：只在回答处于"中间地带"时追问（部分答对但不完整）
		shouldFollowUp := score.ShouldFollowUp &&
			score.Score >= 30 && score.Score < 80 &&
			len(score.KeyPointsMissed) > 0

		if shouldFollowUp {
			followUpText, fErr := o.studentCoach.FollowUp(ctx, state, &askedQuestion, answer, score.Feedback, score.KeyPointsMissed, standard.LearningGoal)
			if fErr == nil {
				followUpText = strings.TrimSpace(followUpText)
				followUpReference := strings.Join(score.KeyPointsMissed, "；")
				followUpAttempt := newTrainingAttempt(userID, q, followUpText, followUpReference, imodel.TrainingAttemptTypeFollowUp, attempt.ID)
				state.TrainingAttempts = append(state.TrainingAttempts, followUpAttempt)
				ic.cb.OnQuestion(asked, "[追问] "+followUpText)

				followUpAnswer, faErr := ic.cb.GetUserAnswer()
				if faErr == nil {
					qa.FollowUpUsed = true
					qa.FollowUpAttemptID = followUpAttempt.ID
					qa.UserAnswer += "\n[追问回答] " + followUpAnswer
					followUpAttempt.RecordAnswer(followUpAnswer)

					// 对追问回答评分并反馈
					followUpScore, fsErr := o.studentCoach.ScoreTrainingAttempt(ctx, followUpAttempt)
					if fsErr == nil {
						ic.cb.OnScore(followUpScore)
					}
				} else if errors.Is(faErr, ErrUserQuit) {
					// 追问阶段退出，记录已有的主回答
					state.QAHistory = append(state.QAHistory, qa)
					userTerminated = true
					ic.cb.OnStageChange("terminated", fmt.Sprintf("学员主动终止训练（已完成 %d/%d 题）", len(state.QAHistory), state.TotalQuestions))
					break
				}
			}
		}

		state.QAHistory = append(state.QAHistory, qa)

		// 动态难度调节（阶段内，由 scheduler 维护；同步到 state 供报告/前端展示）
		sched.record(score.Score)
		state.ConsecutiveRight = sched.consecRight
		state.ConsecutiveWrong = sched.consecWrong

		// 更新薄弱点（长期记忆 → Redis + MySQL）
		for _, skill := range q.Skills {
			_ = o.longTermMem.UpdateWeakPoints(ctx, userID, skill, score.Score)
		}
	}

	ic.userTerminated = userTerminated

	// 终止时设置 session 状态（与顺序版本一致）
	if userTerminated && len(state.QAHistory) == 0 {
		ic.session.Status = imodel.StatusTerminated
		ic.session.UpdatedAt = time.Now()
		ic.cb.OnStageChange("completed", "训练未作答即终止，不生成诊断报告。")
	} else if userTerminated {
		ic.session.Status = imodel.StatusTerminated
	}
	return nil
}

// afterTraining 训练之后的分支：用户未作答即终止 → 直接结束（不生成报告）；否则进入低分巩固/评估。
func (o *Orchestrator) afterTraining(ic *trainingCtx) string {
	if ic.userTerminated && len(ic.trainingState.QAHistory) == 0 {
		return compose.END
	}
	return "weak_review"
}

// nodeWeakReview 阶段 4.5：低分题目巩固
func (o *Orchestrator) nodeWeakReview(ctx context.Context, ic *trainingCtx) error {
	state := ic.trainingState
	userID := ic.userID

	if len(state.QAHistory) == 0 {
		return nil
	}

	var weakQAs []imodel.QAPair
	for _, qa := range state.QAHistory {
		if qa.Score < 60 {
			weakQAs = append(weakQAs, qa)
		}
	}

	if len(weakQAs) == 0 {
		return nil
	}

	ic.cb.OnStageChange("review_weak", fmt.Sprintf("正在对 %d 道低分题目进行巩固...", len(weakQAs)))

	for idx, qa := range weakQAs {
		var reviewContent string

		if qa.Question.Source != "" && qa.Question.Source != "llm" {
			// 题库出题：优先用已有的参考答案，兜底用 RAG 检索
			refAnswer := qa.Question.Reference
			if refAnswer == "" && (o.milvusStore != nil || o.bm25Manager != nil) {
				query := qa.Question.Content
				var docs []*schema.Document
				seen := make(map[string]bool)
				if o.milvusStore != nil {
					milvusDocs, _ := o.milvusStore.RetrieveByUser(ctx, userID, query)
					for _, doc := range milvusDocs {
						if !seen[doc.ID] {
							seen[doc.ID] = true
							docs = append(docs, doc)
						}
					}
				}
				if o.bm25Manager != nil {
					bm25Docs, _ := o.bm25Manager.Retrieve(ctx, userID, query)
					for _, doc := range bm25Docs {
						if !seen[doc.ID] {
							seen[doc.ID] = true
							docs = append(docs, doc)
						}
					}
				}
				if len(docs) > 0 {
					content := docs[0].Content
					if aIdx := strings.Index(content, "\n参考答案："); aIdx >= 0 {
						refAnswer = strings.TrimSpace(content[aIdx+len("\n参考答案："):])
					}
				}
			}

			if refAnswer != "" {
				reviewContent = fmt.Sprintf("**低分题目巩固 %d/%d**\n\n**题目：** %s\n\n**你的得分：** %.0f\n\n**题库参考答案：**\n%s",
					idx+1, len(weakQAs), qa.Question.Content, qa.Score, refAnswer)
			}
		} else {
			// LLM 出题：直接用已有的参考答案
			if qa.Question.Reference != "" {
				reviewContent = fmt.Sprintf("**低分题目巩固 %d/%d**\n\n**题目：** %s\n\n**你的得分：** %.0f\n\n**参考答案：**\n%s",
					idx+1, len(weakQAs), qa.Question.Content, qa.Score, qa.Question.Reference)
			}
		}

		if reviewContent != "" {
			ic.cb.OnQuestion(0, reviewContent)
		}
	}

	ic.cb.OnStageChange("review_weak_done", "低分题目巩固完成")
	return nil
}

// nodeEvaluation 阶段 5：生成评估报告
func (o *Orchestrator) nodeEvaluation(ctx context.Context, ic *trainingCtx) error {
	state := ic.trainingState

	if ic.userTerminated {
		ic.cb.OnStageChange("evaluation", fmt.Sprintf("训练提前终止，正在基于已完成的 %d 道题生成学生能力诊断...", len(state.QAHistory)))
	} else {
		ic.cb.OnStageChange("evaluation", "正在生成学生能力训练评估报告...")
		ic.session.Status = imodel.StatusEvaluated
	}

	report, err := o.abilityEvaluator.Evaluate(ctx, state, ic.abilityStandard, ic.studentProfile, ic.userTerminated)
	if err != nil {
		return fmt.Errorf("orchestrator: evaluate: %w", err)
	}
	profileUpdate, err := o.growthService.UpdateAbilityProfile(ctx, ic.userID, growthservice.GrowthRecordInput{
		SessionID:        report.SessionID,
		TrainingAttempts: report.TrainingAttempts,
		LearningGoal:     report.LearningGoal,
		OverallScore:     report.OverallScore,
		AbilityScores:    report.AbilityScores,
		Strengths:        report.Strengths,
		Weaknesses:       report.Weaknesses,
		Summary:          report.Summary,
		TrainingTime:     report.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("orchestrator: update ability profile: %w", err)
	}
	state.StudentAbilityProfile = profileUpdate.Profile
	ic.abilityProfile = profileUpdate.Profile
	if o.traceService != nil {
		_ = o.traceService.SaveAbilityChanges(ctx, ic.session.ID, ic.abilityBefore, profileUpdate.Profile.AbilityScores)
	}
	ic.report = report
	ic.session.Report = report

	reportMD := agent.FormatReport(report)
	ic.cb.OnReport(reportMD)
	return nil
}

// nodeGrowthPlan 阶段 6：生成成长计划 + 持久化训练记录。
func (o *Orchestrator) nodeGrowthPlan(ctx context.Context, ic *trainingCtx) error {
	ic.cb.OnStageChange("growth_plan", "正在生成学生能力提升计划...")

	reviewPlan, err := o.growthPlanner.Plan(ctx, ic.report)
	if err != nil {
		return fmt.Errorf("orchestrator: review plan: %w", err)
	}
	ic.session.ReviewPlan = reviewPlan
	ic.session.Status = imodel.StatusCompleted
	ic.session.UpdatedAt = time.Now()

	planMD := agent.FormatReviewPlan(reviewPlan)
	ic.cb.OnReviewPlan(planMD)

	// 能力画像与 GrowthRecord 已在 evaluation 节点由 StudentGrowthService 保存；
	// 此处只补齐现有记录中的成长计划 JSON。
	if o.mysqlStore != nil {
		reportJSON, _ := json.Marshal(ic.report)
		planJSON, _ := json.Marshal(reviewPlan)
		_ = o.mysqlStore.SaveInterviewRecord(ctx, ic.userID, memory.InterviewRecord{
			SessionID:    ic.session.ID,
			LearningGoal: ic.abilityStandard.LearningGoal,
			OverallScore: ic.report.OverallScore,
			Date:         time.Now(),
		}, string(reportJSON), string(planJSON))
	}

	ic.cb.OnStageChange("completed", "本轮学生能力训练已完成！")
	return nil
}

func newTrainingAttempt(studentID string, planned imodel.PlannedQuestion, question, reference, attemptType, parentAttemptID string) *imodel.TrainingAttempt {
	now := time.Now()
	return &imodel.TrainingAttempt{
		ID:              uuid.New().String(),
		StudentID:       studentID,
		SkillName:       imodel.SkillNameForQuestion(planned),
		TrainingTask:    planned.Content,
		Question:        question,
		ReferenceAnswer: reference,
		Rubric:          imodel.EvaluationRubricForQuestion(planned),
		AbilityChanges:  map[string]float64{},
		Status:          imodel.TrainingAttemptStatusPresented,
		CreatedAt:       now,
		UpdatedAt:       now,
		ParentAttemptID: parentAttemptID,
		AttemptType:     attemptType,
	}
}

func traceIntentForGoal(goal string) string {
	ability := imodel.NormalizeAbilityDimension(goal)
	if ability == "" {
		return "ability training"
	}
	return "improve " + ability
}

func traceSkillDecision(goal string) (string, string) {
	ability := imodel.NormalizeAbilityDimension(goal)
	if ability == "" {
		return "", ""
	}
	return imodel.AbilitySkillName(ability), fmt.Sprintf("learning goal targets %s ability", ability)
}

func traceAttemptDecisionReason(attempt *imodel.TrainingAttempt) string {
	if attempt == nil {
		return "selected by the training plan"
	}
	ability := imodel.NormalizeAbilityDimension(attempt.SkillName)
	if ability == "" && len(attempt.Rubric) > 0 {
		ability = imodel.NormalizeAbilityDimension(attempt.Rubric[0].Ability)
	}
	if ability == "" {
		return "selected by the training plan"
	}
	return fmt.Sprintf("training task targets %s ability", ability)
}

func traceAbilitySummaries(scores map[string]float64) []string {
	summaries := make([]string, 0, len(scores))
	for _, ability := range imodel.CoreAbilityDimensions() {
		if score, ok := scores[ability]; ok {
			summaries = append(summaries, fmt.Sprintf("%s=%.2f", ability, score))
		}
	}
	return summaries
}

func copyTraceScores(scores map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(scores))
	for ability, score := range scores {
		result[ability] = score
	}
	return result
}

func (o *Orchestrator) markTraceStatus(ctx context.Context, sessionID, status string) {
	if o.traceService != nil {
		_ = o.traceService.UpdateStatus(ctx, sessionID, status)
	}
}

// formatRAGDocs 格式化 RAG 召回的文档作为出题参考
func formatRAGDocs(docs []*schema.Document) string {
	var sb strings.Builder
	for i, doc := range docs {
		docID := doc.ID
		if docID == "" {
			docID = fmt.Sprintf("rag_%d", i+1)
		}
		sb.WriteString(fmt.Sprintf("### 题库原题 [%s]\n%s\n\n", docID, doc.Content))
	}
	return sb.String()
}

// collectStandardAbilities 收集能力标准中的所有能力关键词（小写）。
func collectStandardAbilities(standard *imodel.AbilityStandard) []string {
	var skills []string
	for _, s := range standard.TargetAbilities {
		skills = append(skills, strings.ToLower(s.Name))
	}
	for _, s := range standard.ExtensionAbilities {
		skills = append(skills, strings.ToLower(s.Name))
	}
	for _, t := range standard.KeyTopics {
		skills = append(skills, strings.ToLower(t))
	}
	return skills
}

// isWeakPointRelevant 判断薄弱点是否和当前能力标准相关（包含关系匹配）。
func isWeakPointRelevant(topic string, standardAbilities []string) bool {
	topicLower := strings.ToLower(topic)
	for _, skill := range standardAbilities {
		if strings.Contains(topicLower, skill) || strings.Contains(skill, topicLower) {
			return true
		}
	}
	return false
}
