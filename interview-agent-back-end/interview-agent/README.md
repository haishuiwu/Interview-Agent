# StudentCoach Backend

StudentCoach 是面向学生能力提升的 AI Growth Agent。本目录为其 Go 后端，负责训练编排、业务规则、数据服务、长期能力画像、检索、实时交互和自动评测。

如果你第一次了解项目，请先阅读[项目首页](../../README.md)。

## 后端职责

- 接收学习目标与学生画像，创建训练会话；
- 使用 Eino Graph 编排完整训练流程；
- 根据学生能力画像、Skill 和 RAG 案例规划训练任务；
- 支持 StudentCoach 多轮追问、即时反馈和动态难度；
- 聚合能力评价，更新长期画像并保存成长记录；
- 为前端提供认证、WebSocket、语音和报告接口；
- 通过统一 Tool Registry 向 Agent 提供学生成长数据能力。

## 运行原则

后端将模型推理与业务决策分开：

- LLM 分析学生表现，提供命中点、遗漏点和评价依据；
- Go 服务计算训练指标、聚合最终能力分并控制画像更新；
- Skill 描述训练策略，不直接访问数据库；
- Tool 只提供数据能力，成长与评价规则由 StudentGrowthService 控制；
- Memory 保存会话上下文和长期能力画像；
- Graph 继续沿用既有拓扑，不在运行时创建新的流程。

## 训练流程

1. AbilityAnalyzer 根据学习目标形成能力标准。
2. StudentProfileAnalyzer 分析学生现状、优势和能力差距。
3. QuestionPlanner 结合能力画像、Skill、RAG 案例和难度规划任务。
4. StudentCoach 组织训练，通过追问观察思考过程并给出即时反馈。
5. AbilityEvaluator 生成评价依据，Go 服务完成确定性聚合。
6. StudentGrowthService 更新能力画像并保存 GrowthRecord。
7. GrowthPlanner 根据本轮结果生成下一步成长计划。

Eino Graph 的流程与拓扑没有改变；Agent 文件和节点标识使用学生能力训练语义，历史命令名仅在 CLI adapter 中保留。

## 长期能力画像

StudentAbilityProfile 跨训练维护学生的成长状态，包含：

- 学生标识与能力概述；
- 逻辑思维、沟通表达、问题解决、批判性思维和反思能力得分；
- 当前优势和短板；
- 每次训练前后的能力变化；
- 最近训练时间。

首次训练结束后，系统会创建能力画像。后续训练会先读取画像，并优先选择当前弱项对应的训练 Skill；画像也会影响任务推荐和难度。

## 训练 Skill

| Skill | 训练重点 |
|---|---|
| logical-thinking | 关系识别、推理过程和结论检查 |
| communication-training | 信息组织、结构化表达和沟通意识 |
| problem-solving | 问题定义、方案设计、验证和迭代 |
| critical-thinking | 证据判断、假设识别和多角度比较 |
| reflection-training | 复盘归因、方法总结和迁移应用 |

每个 Skill 定义适用场景、训练目标、教练行为规则和评价维度。

## 教育 Tool Calling

统一 Tool Registry 提供四类数据能力：

| 类别 | 能力 |
|---|---|
| 学生信息 | 查询学生画像和长期能力画像 |
| 成长历史 | 查询历史训练记录和最近评价 |
| 训练资源 | 按能力短板检索案例并推荐任务 |
| 成长写入 | 更新能力画像并保存训练记录 |

Agent 根据学生意图决定是否调用工具。学生身份由认证后的运行时上下文注入，模型不能通过参数切换当前学生。

## 数据与基础能力

| 模块 | 作用 |
|---|---|
| Eino Graph | 编排训练节点和条件分支 |
| Milvus | 保存并检索训练案例向量 |
| BM25 | 提供关键词召回 |
| Redis | 缓存会话和画像数据 |
| MySQL | 持久化用户、会话和成长数据 |
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
| 启动兼容训练命令 | go run cmd/main.go interview |
| 加载内置训练数据 | go run cmd/main.go load-data |
| 加载指定训练资料 | go run cmd/main.go load-data 文件路径 |
| 查看 RAG 评估参数 | go run cmd/main.go eval -h |

interview 是历史兼容命令名，新的业务语义仍为学生能力训练。

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

## 测试与评测

- 运行 go test ./...，执行完整后端测试。
- 运行 go test -v ./evaluation，查看 StudentCoach 离线评测报告。
- 运行 go run cmd/main.go eval -h，查看 RAG 评估工具。

Evaluation Benchmark 使用 mock LLM，覆盖 Skill 选择、Tool 调用、能力诊断和成长闭环。测试数据位于 evaluation/testdata，详细口径见[评测说明](evaluation/README.md)。

## 配置

完整环境变量及语音开关见 [.env.example](.env.example)。常用配置包括模型与嵌入模型、Milvus、Redis、MySQL、重排策略、JWT、MCP 和 Speech。

不要提交真实 API Key、JWT 密钥、音频内容或完整语音转写。

## 前端

Web 客户端位于[前端子项目](../../interview-agent-web/interview-agent-web)。开发环境通过 Vite 将 API 和 WebSocket 请求代理到本后端。

## 问题反馈

如需反馈问题或提出改进建议，请使用 [GitHub Issues](https://github.com/haishuiwu/Interview-Agent/issues)。
