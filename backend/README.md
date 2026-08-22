# Student-Coach Backend

Student-Coach 是 AI 自适应学习训练与知识掌握诊断系统。本目录为其 Go 后端，负责学习状态分析、训练规划、题目检索、作答评价、动态追问、掌握度更新和下一轮训练。

如果你第一次了解项目，请先阅读[项目首页](../README.md)。

## 后端职责

- 接收学科、章节、学习目标与当前学习状态，创建训练会话；
- 使用 Eino Graph 编排完整训练流程；
- 根据知识掌握画像、Skill 和 RAG 题目规划训练任务；
- 支持受限动态追问、即时反馈和确定性难度调整；
- 聚合作答证据，更新知识掌握画像并保存学习轨迹；
- 为前端提供认证、WebSocket、语音和报告接口；
- 通过统一 Tool Registry 向 Agent 提供学习状态事实。

## 运行原则

后端将模型推理与业务决策分开：

- LLM 分析学生表现，提供命中点、遗漏点和评价依据；
- Go 服务计算训练指标、聚合知识点掌握度并控制画像更新；
- Skill 描述训练策略，不直接访问数据库；
- Tool 只提供数据能力，掌握度与评价规则由 StudentGrowthService 控制；
- Memory 保存会话状态和长期知识掌握画像；
- Graph 继续沿用既有拓扑，不在运行时创建新的流程。

## 训练流程

1. AbilityAnalyzer 根据学科与学习目标提取待训练知识点。
2. StudentProfileAnalyzer 结合历史证据分析当前学习状态和薄弱点。
3. QuestionPlanner 结合知识掌握画像、Skill、RAG 题目和难度规划训练。
4. Student-Coach 提问并通过 WebSocket 等待真实作答，必要时进行一次受控追问。
5. AbilityEvaluator 提取正确点、错误点和遗漏点，Go 服务完成确定性聚合。
6. StudentGrowthService 更新知识点掌握度并保存学习轨迹。
7. GrowthPlanner 根据本轮诊断生成下一轮学习路径计划。

Eino Graph 的流程与拓扑没有改变；Agent 文件和节点标识使用知识掌握训练语义，历史命令名仅在 CLI adapter 中保留。

## 长期知识掌握画像

StudentAbilityProfile 跨训练维护学生的知识掌握状态，包含：

- 学生标识与学习状态概述；
- 各知识点掌握度与近期作答表现；
- 已掌握内容和薄弱知识点；
- 每次训练前后的学习状态变化；
- 最近训练时间。

首次训练结束后，系统会创建知识掌握画像。后续训练先读取画像，并优先处理薄弱知识点；画像同时影响题目检索、支架方式和难度。

## 训练 Skill

| Skill | 训练重点 |
|---|---|
| concept-tutor | 概念讲解与理解核验 |
| quick-quiz | 快速诊断当前掌握状态 |
| error-review | 基于错误证据定位错因并复盘 |
| guided-practice | 通过分步支架完成练习 |
| knowledge-compare | 对比易混知识并检查边界 |

每个 Skill 定义适用场景、训练目标、教练行为规则和评价维度。

## 教育 Tool Calling

统一 Tool Registry 提供四类数据能力：

| 类别 | 能力 |
|---|---|
| 学生信息 | 查询当前学习状态和知识掌握画像 |
| 学习轨迹 | 查询历史训练记录和最近评价 |
| 训练资源 | 按薄弱知识点检索题目并推荐任务 |
| 状态写入 | 由可信 Go 流程更新掌握度并保存训练记录 |

Agent 根据学生意图决定是否调用工具。学生身份由认证后的运行时上下文注入，模型不能通过参数切换当前学生。

## 数据与基础能力

| 模块 | 作用 |
|---|---|
| Eino Graph | 编排训练节点和条件分支 |
| Milvus | 保存并检索训练案例向量 |
| BM25 | 提供关键词召回 |
| Redis | 缓存会话和画像数据 |
| MySQL | 持久化用户、会话和学习轨迹数据 |
| MCP | 保留网页内容提取和 GitHub 搜索能力 |
| WebSocket | 承载训练会话与流式消息 |
| Speech | 提供 TTS、实时 ASR 和降级识别 |

## 环境要求

- Go 1.26.1，或启用 Go 自动工具链；
- Docker Compose；
- 通义千问 DashScope API Key；
- Node.js，用于现有网页 MCP 能力和前端开发。

## 本地启动

1. 将 .env.example 复制为 .env。
2. 在 .env 中填写 DASHSCOPE_API_KEY。
3. 运行 docker compose up -d。
4. 等待 Milvus、Redis、MySQL、etcd 和 MinIO 就绪。
5. 运行 go run cmd/main.go web。
6. 访问 http://localhost:9090/health 确认服务正常。

默认端口：

| 服务 | 端口 |
|---|---:|
| Web 与 API | 9090 |
| Milvus | 19530 |
| Redis | 36379 |
| MySQL | 33306 |
| MinIO API | 9000 |
| MinIO Console | 9001 |

## 程序入口

| 用途 | 命令 |
|---|---|
| 启动 Web 服务 | go run cmd/main.go web |
| 进入聊天模式 | go run cmd/main.go chat |
| 启动自适应训练 | go run cmd/main.go train |
| 历史兼容命令 | go run cmd/main.go interview |
| 加载内置训练数据 | go run cmd/main.go load-data |
| 加载指定训练资料 | go run cmd/main.go load-data 文件路径 |
| 查看 RAG 评估参数 | go run cmd/main.go eval -h |

`interview` 仅是历史兼容命令名；新调用统一使用 `train`，产品展示不再使用旧名称。

## 接口概览

| 接口 | 用途 |
|---|---|
| GET /health | 服务健康检查 |
| POST /api/register | 用户注册 |
| POST /api/login | 用户登录 |
| /ws | 训练会话 WebSocket |
| GET /api/speech/capabilities | 查询语音能力 |
| POST /api/speech/tts | 文本转语音 |
| /ws/speech/asr | 实时语音识别 WebSocket |

新的训练请求使用 learning_goal 和 student_profile。后端仍在兼容 DTO 中接收旧协议，并在进入核心运行时前转换；新增调用方不应继续使用历史字段。

## 语音能力

语音默认关闭，不影响纯文字训练。启用前需要：

- 配置 DashScope API Key；
- 使用随机高强度 JWT_SECRET；
- 通过 WEB_ALLOWED_ORIGINS 设置精确的前端 Origin；
- 根据需要分别启用 TTS 和 ASR；
- 检查并发、音频大小、时长和超时限制。

浏览器不会持有供应商 API Key，ASR 结果只写入可编辑草稿，不会自动提交答案。

发布与隐私约束：

- 第一批灰度：只开启 Coach 朗读（TTS），确认稳定后再逐步开放 ASR；需要全局回滚时设置 `SPEECH_ENABLED=false`。
- 语音日志不保存音频正文或完整转写；`user_id` 使用 HMAC 后再写入诊断日志。

## 测试与评测

- 运行 go test ./...，执行完整后端测试。
- 运行 go test -v ./evaluation，查看 Student-Coach 离线评测报告。
- 运行 go run cmd/main.go eval -h，查看 RAG 评估工具。

Evaluation Benchmark 使用 mock LLM，覆盖 Skill 选择、Tool 调用、知识掌握诊断和学习闭环。测试数据位于 evaluation/testdata，详细口径见[评测说明](evaluation/README.md)。

## 配置

完整环境变量及语音开关见 [.env.example](.env.example)。常用配置包括模型与嵌入模型、Milvus、Redis、MySQL、重排策略、JWT、MCP 和 Speech。

不要提交真实 API Key、JWT 密钥、音频内容或完整语音转写。

## 前端

Web 客户端位于[前端子项目](../web)。开发环境通过 Vite 将 API 和 WebSocket 请求代理到本后端。

## 问题反馈

如需反馈问题或提出改进建议，请使用当前仓库的 Issues。
