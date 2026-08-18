/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package graph 用 Eino compose.Graph 把 6 阶段编排成有向图，串联各 Agent 完成面试全流程
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
)

// ErrUserQuit 用户主动终止面试
var ErrUserQuit = errors.New("用户主动终止面试")

// Orchestrator 面试流程编排器，管理各 Agent 的协作
type Orchestrator struct {
	jdAnalyzer      *agent.JDAnalyzer
	resumeMatcher   *agent.ResumeMatcher
	questionPlanner *agent.QuestionPlanner
	interviewer     *agent.Interviewer
	evaluator       *agent.Evaluator
	reviewPlanner   *agent.ReviewPlanner

	// 记忆系统
	shortTermMem *memory.ShortTermMemory
	longTermMem  *memory.LongTermMemory

	// RAG 多路召回
	milvusStore *rag.MilvusStore   // Milvus 向量存储（支持按用户过滤）
	bm25Manager *rag.BM25Manager   // BM25 按用户管理
	reranker    rag.RerankStrategy // 重排策略（LLM / cross-encoder / none，可切换）

	// 持久化
	mysqlStore *memory.MySQLStore // MySQL（保存面试记录）
}

// OrchestratorConfig 编排器配置
type OrchestratorConfig struct {
	ChatModel      model.ChatModel
	Store          memory.Store        // Redis+MySQL 组合存储
	MilvusStore    *rag.MilvusStore    // Milvus 向量存储（支持按用户检索/删除）
	BM25Manager    *rag.BM25Manager    // BM25 按用户管理
	MySQLStore     *memory.MySQLStore  // MySQL 直接引用（保存面试记录）
	GitHubSearcher *mcp.GitHubSearcher // GitHub MCP 搜索器（可选，用于复习计划推荐）
	RerankerType   string              // 重排策略：cross-encoder（默认）/ llm / none
	RerankModel    string              // cross-encoder 重排模型名（默认 gte-rerank-v2）
	APIKey         string              // DashScope API Key（cross-encoder rerank 调用用）
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
	o := &Orchestrator{
		jdAnalyzer:      agent.NewJDAnalyzer(cfg.ChatModel),
		resumeMatcher:   agent.NewResumeMatcher(cfg.ChatModel),
		questionPlanner: agent.NewQuestionPlanner(cfg.ChatModel),
		interviewer:     agent.NewInterviewer(cfg.ChatModel),
		evaluator:       agent.NewEvaluator(cfg.ChatModel),
		reviewPlanner:   agent.NewReviewPlanner(cfg.ChatModel),
		shortTermMem:    memory.NewShortTermMemory(20),
		longTermMem:     memory.NewLongTermMemory(cfg.Store),
		milvusStore:     cfg.MilvusStore,
		bm25Manager:     cfg.BM25Manager,
		reranker:        newReranker(cfg),
		mysqlStore:      cfg.MySQLStore,
	}

	// 设置 GitHub MCP 搜索器（可选）
	if cfg.GitHubSearcher != nil {
		o.reviewPlanner.SetGitHubSearcher(cfg.GitHubSearcher)
	}

	return o
}

// InterviewCallbacks 面试过程回调，用于 CLI/Web 等不同界面
type InterviewCallbacks struct {
	OnStageChange func(stage string, msg string)
	OnQuestion    func(questionNum int, content string)
	OnScore       func(score *agent.AnswerScore)
	OnReport      func(report string)
	OnReviewPlan  func(plan string)
	GetUserAnswer func() (string, error)
}

// interviewCtx 单次面试的上下文持有者。
// 注意：不放进 graph 的数据流，而是由各节点闭包捕获共享——graph 只负责编排节点的执行顺序与分支。
type interviewCtx struct {
	jdText     string
	resumeText string
	userID     string
	cb         *InterviewCallbacks

	session        *imodel.Session
	jdAnalysis     *imodel.JDAnalysis
	resume         *imodel.Resume
	matchResult    *imodel.ResumeMatchResult
	plan           *imodel.QuestionPlan
	state          *imodel.InterviewState
	report         *imodel.EvaluationReport
	userTerminated bool
}

// RunInterview 执行完整面试流程：构建并编译一张 compose.Graph（节点闭包捕获本次面试上下文），驱动它跑完。
func (o *Orchestrator) RunInterview(ctx context.Context, jdText string, resumeText string, userID string, cb *InterviewCallbacks) (*imodel.Session, error) {
	ic := &interviewCtx{jdText: jdText, resumeText: resumeText, userID: userID, cb: cb}

	g := compose.NewGraph[string, string]()

	_ = g.AddLambdaNode("jd_analysis", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeJDAnalysis(ctx, ic)
	}))
	_ = g.AddLambdaNode("resume_match", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeResumeMatch(ctx, ic)
	}))
	_ = g.AddLambdaNode("question_plan", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeQuestionPlan(ctx, ic)
	}))
	_ = g.AddLambdaNode("interview", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeInterview(ctx, ic)
	}))
	_ = g.AddLambdaNode("weak_review", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeWeakReview(ctx, ic)
	}))
	_ = g.AddLambdaNode("evaluation", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeEvaluation(ctx, ic)
	}))
	_ = g.AddLambdaNode("review_plan", compose.InvokableLambda(func(ctx context.Context, _ string) (string, error) {
		return "", o.nodeReviewPlan(ctx, ic)
	}))

	_ = g.AddEdge(compose.START, "jd_analysis")
	_ = g.AddEdge("jd_analysis", "resume_match")
	_ = g.AddEdge("resume_match", "question_plan")
	_ = g.AddEdge("question_plan", "interview")
	// interview 之后的条件分支：用户未作答即终止 → END；否则 → weak_review
	_ = g.AddBranch("interview", compose.NewGraphBranch(
		func(ctx context.Context, _ string) (string, error) { return o.afterInterview(ic), nil },
		map[string]bool{"weak_review": true, compose.END: true}))
	_ = g.AddEdge("weak_review", "evaluation")
	_ = g.AddEdge("evaluation", "review_plan")
	_ = g.AddEdge("review_plan", compose.END)

	runnable, err := g.Compile(ctx)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: compile graph: %w", err)
	}
	log.Printf("[Orchestrator] 面试流程 compose.Graph 已编译，开始执行")

	if _, err := runnable.Invoke(ctx, ""); err != nil {
		return nil, err
	}

	return ic.session, nil
}

// ============================================================
// 各阶段节点（读写 interviewCtx，调回调推送前端；返回 error 会中断 graph）
// ============================================================

// nodeJDAnalysis 阶段 1：JD 分析
func (o *Orchestrator) nodeJDAnalysis(ctx context.Context, ic *interviewCtx) error {
	ic.session = &imodel.Session{
		ID:        uuid.New().String(),
		UserID:    ic.userID,
		Status:    imodel.StatusInit,
		CreatedAt: time.Now(),
	}

	ic.cb.OnStageChange("jd_analysis", "正在解析目标教师岗位或教学能力考核标准...")

	jdAnalysis, err := o.jdAnalyzer.Analyze(ctx, ic.jdText)
	if err != nil {
		return fmt.Errorf("orchestrator: jd analysis: %w", err)
	}
	ic.jdAnalysis = jdAnalysis
	ic.session.JDAnalysis = jdAnalysis
	ic.session.Status = imodel.StatusJDAnalyzed

	ic.cb.OnStageChange("jd_analysis_done", fmt.Sprintf("目标标准解析完成：%s", jdAnalysis.Position))
	return nil
}

// nodeResumeMatch 阶段 2：简历匹配
func (o *Orchestrator) nodeResumeMatch(ctx context.Context, ic *interviewCtx) error {
	ic.cb.OnStageChange("resume_match", "正在建立学员教学能力训练起点...")

	ic.resume = &imodel.Resume{RawText: ic.resumeText}
	ic.session.Resume = ic.resume

	matchResult, err := o.resumeMatcher.Match(ctx, ic.jdAnalysis, ic.resume)
	if err != nil {
		return fmt.Errorf("orchestrator: resume match: %w", err)
	}
	ic.matchResult = matchResult
	ic.session.MatchResult = matchResult
	ic.session.Status = imodel.StatusResumeMatched

	ic.cb.OnStageChange("resume_match_done", fmt.Sprintf("教学档案诊断完成，当前证据覆盖度：%.0f%%", matchResult.OverallScore))
	return nil
}

// nodeQuestionPlan 阶段 2.5 + 3：读取历史薄弱点 + 出题规划（Phase1 方向 + Phase2 检索/组装）
func (o *Orchestrator) nodeQuestionPlan(ctx context.Context, ic *interviewCtx) error {
	jdAnalysis := ic.jdAnalysis
	matchResult := ic.matchResult
	userID := ic.userID

	// ===== 阶段 2.5：读取历史薄弱点（长期记忆），并按当前 JD 过滤 =====
	var weakPointsContext string
	weakPoints := o.longTermMem.GetWeakPoints(ctx, userID)
	if len(weakPoints) > 0 {
		jdSkills := collectJDSkills(jdAnalysis)
		var wpLines []string
		for _, wp := range weakPoints {
			if isWeakPointRelevant(wp.Topic, jdSkills) {
				wpLines = append(wpLines, fmt.Sprintf("- %s：历史得分 %.0f，被考察 %d 次，答错 %d 次",
					wp.Topic, wp.Score, wp.HitCount, wp.WrongCount))
			}
		}
		if len(wpLines) > 0 {
			weakPointsContext = strings.Join(wpLines, "\n")
			ic.cb.OnStageChange("memory_loaded", fmt.Sprintf("已加载 %d 个与当前目标相关的历史待训练能力，将针对性练习", len(wpLines)))
		}
	}

	// ===== 阶段 3 Phase 1：规划出题方向 =====
	ic.cb.OnStageChange("question_plan", "正在规划个性化教学能力训练方向...")

	dirPlan, err := o.questionPlanner.PlanDirections(ctx, jdAnalysis, matchResult, weakPointsContext)
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
		ic.cb.OnStageChange("rag_retrieval", "正在从教师训练题库检索教育理论题目...")

		for i, dir := range dirPlan.Directions {
			// 教学实践与课堂情境需要结合学员档案动态生成；教育理论可优先检索题库。
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

			// 用户私有题库没有命中时，再回退到系统内置教师题库；私有数据仍按 userID 隔离。
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
		ic.cb.OnStageChange("question_assemble", fmt.Sprintf("正在为 %d 个方向生成教学训练题目...", len(unmatchedDirs)))
		unmatchedPlan := &imodel.QuestionDirectionPlan{Directions: unmatchedDirs}
		emptyDocs := make([]string, len(unmatchedDirs)) // 全部无匹配
		assembled, aErr := o.questionPlanner.AssembleQuestions(ctx, jdAnalysis, matchResult, unmatchedPlan, emptyDocs)
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
		Distribution:   imodel.QuestionDistrib{Basic: basicCount, Experience: expCount, Design: designCount},
		Questions:      allQuestions,
	}
	ic.plan = plan
	ic.session.QuestionPlan = plan
	ic.session.Status = imodel.StatusPlanned

	ic.cb.OnStageChange("question_plan_done", fmt.Sprintf("训练题池完成，共 %d 道题（教育理论%d/教学实践%d/课堂情境%d）",
		plan.TotalQuestions, basicCount, expCount, designCount))
	return nil
}

// nodeInterview 阶段 4：模拟面试（含追问、动态难度调节、薄弱点更新，人在环阻塞交互）
func (o *Orchestrator) nodeInterview(ctx context.Context, ic *interviewCtx) error {
	jdAnalysis := ic.jdAnalysis
	plan := ic.plan
	userID := ic.userID

	ic.cb.OnStageChange("interview", "教学能力训练正式开始！")

	// 训练分三个阶段顺序进行：教育理论 → 教学实践 → 课堂情境。
	// 阶段化取题与阶段内难度调节由 stageScheduler 负责（见 stage_scheduler.go）：
	// 每阶段从候选池按当前难度自适应抽取固定道数；进入新阶段时难度重置为 medium，不继承上一阶段。
	sched := newStageScheduler(defaultStages, plan.Questions,
		func(cur imodel.DifficultyLevel, consecRight, consecWrong int) imodel.DifficultyLevel {
			return o.questionPlanner.AdjustDifficulty(&imodel.InterviewState{
				CurrentDifficulty: cur,
				ConsecutiveRight:  consecRight,
				ConsecutiveWrong:  consecWrong,
			})
		})

	state := &imodel.InterviewState{
		SessionID:         ic.session.ID,
		TotalQuestions:    sched.totalToAsk(),
		CurrentDifficulty: imodel.DifficultyMedium,
	}
	ic.state = state
	ic.session.InterviewState = state
	ic.session.Status = imodel.StatusInterviewing

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

		// 面试官提问
		questionText, err := o.interviewer.AskQuestion(ctx, state, &q, jdAnalysis.Position)
		if err != nil {
			return fmt.Errorf("orchestrator: ask question %d: %w", asked, err)
		}
		// 标注题目来源
		if q.Source != "" && q.Source != "llm" {
			questionText += fmt.Sprintf("\n\n`[来源: 题库 %s]`", q.Source)
		} else {
			questionText += "\n\n`[来源: LLM 出题]`"
		}

		// 发送题目到前端
		ic.cb.OnQuestion(asked, questionText)

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

		// 评分
		score, err := o.interviewer.ScoreAnswer(ctx, &q, answer)
		if err != nil {
			return fmt.Errorf("orchestrator: score answer %d: %w", asked, err)
		}
		ic.cb.OnScore(score)

		// 更新候选人动态画像
		updatedProfile, profileErr := o.interviewer.UpdateCandidateProfile(ctx, state.CandidateProfile, asked, &q, score)
		if profileErr != nil {
			log.Printf("[Profile] 画像更新失败（不影响主流程）: %v", profileErr)
		} else {
			state.CandidateProfile = updatedProfile
		}

		// 记录问答
		qa := imodel.QAPair{
			Question:   q,
			UserAnswer: answer,
			Score:      score.Score,
			Feedback:   score.Feedback,
		}

		// 追问逻辑：只在回答处于"中间地带"时追问（部分答对但不完整）
		shouldFollowUp := score.ShouldFollowUp &&
			score.Score >= 30 && score.Score < 80 &&
			len(score.KeyPointsMissed) > 0

		if shouldFollowUp {
			followUpText, fErr := o.interviewer.FollowUp(ctx, state, &q, answer, score.Feedback, score.KeyPointsMissed, jdAnalysis.Position)
			if fErr == nil {
				ic.cb.OnQuestion(asked, "[追问] "+followUpText)

				followUpAnswer, faErr := ic.cb.GetUserAnswer()
				if faErr == nil {
					qa.FollowUpUsed = true
					qa.UserAnswer += "\n[追问回答] " + followUpAnswer

					// 对追问回答评分并反馈
					followUpScore, fsErr := o.interviewer.ScoreAnswer(ctx, &q, followUpAnswer)
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

// afterInterview interview 之后的分支：用户未作答即终止 → 直接结束（不生成报告）；否则进入低分巩固/评估。
func (o *Orchestrator) afterInterview(ic *interviewCtx) string {
	if ic.userTerminated && len(ic.state.QAHistory) == 0 {
		return compose.END
	}
	return "weak_review"
}

// nodeWeakReview 阶段 4.5：低分题目巩固
func (o *Orchestrator) nodeWeakReview(ctx context.Context, ic *interviewCtx) error {
	state := ic.state
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
func (o *Orchestrator) nodeEvaluation(ctx context.Context, ic *interviewCtx) error {
	state := ic.state

	if ic.userTerminated {
		ic.cb.OnStageChange("evaluation", fmt.Sprintf("训练提前终止，正在基于已完成的 %d 道题生成教学能力诊断...", len(state.QAHistory)))
	} else {
		ic.cb.OnStageChange("evaluation", "正在生成教学能力训练评估报告...")
		ic.session.Status = imodel.StatusEvaluated
	}

	report, err := o.evaluator.Evaluate(ctx, state, ic.jdAnalysis.Position, ic.resume.Name, ic.userTerminated)
	if err != nil {
		return fmt.Errorf("orchestrator: evaluate: %w", err)
	}
	ic.report = report
	ic.session.Report = report

	reportMD := agent.FormatReport(report)
	ic.cb.OnReport(reportMD)
	return nil
}

// nodeReviewPlan 阶段 6：生成复习计划 + 持久化面试记录
func (o *Orchestrator) nodeReviewPlan(ctx context.Context, ic *interviewCtx) error {
	ic.cb.OnStageChange("review_plan", "正在生成教学能力提升计划...")

	reviewPlan, err := o.reviewPlanner.Plan(ctx, ic.report)
	if err != nil {
		return fmt.Errorf("orchestrator: review plan: %w", err)
	}
	ic.session.ReviewPlan = reviewPlan
	ic.session.Status = imodel.StatusCompleted
	ic.session.UpdatedAt = time.Now()

	planMD := agent.FormatReviewPlan(reviewPlan)
	ic.cb.OnReviewPlan(planMD)

	// ===== 持久化面试记录 =====
	_ = o.longTermMem.AddInterviewRecord(ctx, ic.userID, memory.InterviewRecord{
		SessionID:    ic.session.ID,
		Position:     ic.jdAnalysis.Position,
		OverallScore: ic.report.OverallScore,
		Date:         time.Now(),
	})

	if o.mysqlStore != nil {
		reportJSON, _ := json.Marshal(ic.report)
		planJSON, _ := json.Marshal(reviewPlan)
		_ = o.mysqlStore.SaveInterviewRecord(ctx, ic.userID, memory.InterviewRecord{
			SessionID:    ic.session.ID,
			Position:     ic.jdAnalysis.Position,
			OverallScore: ic.report.OverallScore,
			Date:         time.Now(),
		}, string(reportJSON), string(planJSON))
	}

	ic.cb.OnStageChange("completed", "本轮教学能力训练已完成！")
	return nil
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

// collectJDSkills 收集 JD 中的所有技能关键词（小写）
func collectJDSkills(jd *imodel.JDAnalysis) []string {
	var skills []string
	for _, s := range jd.RequiredSkills {
		skills = append(skills, strings.ToLower(s.Name))
	}
	for _, s := range jd.PreferredSkills {
		skills = append(skills, strings.ToLower(s.Name))
	}
	for _, t := range jd.KeyTopics {
		skills = append(skills, strings.ToLower(t))
	}
	return skills
}

// isWeakPointRelevant 判断薄弱点是否和当前 JD 技能相关（包含关系匹配）
func isWeakPointRelevant(topic string, jdSkills []string) bool {
	topicLower := strings.ToLower(topic)
	for _, skill := range jdSkills {
		if strings.Contains(topicLower, skill) || strings.Contains(skill, topicLower) {
			return true
		}
	}
	return false
}
