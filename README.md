# StudentCoach

面向学生长期成长的 AI 能力训练教练。

StudentCoach 根据学生的学习目标、当前能力和历史训练记录组织个性化训练。它通过任务、追问和反馈帮助学生发现问题，并在每轮训练后更新能力画像，让下一次训练能够从真实成长状态继续。

## 项目价值

多数学习助手只回答当前问题，难以判断学生长期缺什么、练过什么、下一步应该练什么。StudentCoach 将一次对话扩展为持续成长闭环：

- 训练前了解目标和已有能力；
- 训练中根据表现调整任务、追问方向和难度；
- 训练后生成有依据的能力评价与成长建议；
- 下一次训练读取历史画像，优先训练当前短板。

系统用于能力训练和形成性反馈，不替代教师评价，也不作升学、招聘或录用决策。

## 适用场景

- 学生希望提升表达能力，但不知道问题出在结构、论据还是沟通方式；
- 学生面对开放问题时缺少分析路径，需要练习拆解、方案设计与验证；
- 学生希望训练逻辑思维、批判性思维或复盘能力；
- 教师或学习产品需要为学生提供可持续、可追踪的个性化训练。

## 核心能力

### 个性化训练

StudentCoach 先理解学习目标，再结合学生画像选择训练方式。目标不明确时，系统会根据历史能力短板推荐优先训练方向。

### 五维能力画像

系统持续维护以下能力：

| 能力维度 | 关注点 |
|---|---|
| 逻辑思维 | 关系识别、推理过程与结论一致性 |
| 沟通表达 | 信息完整性、结构清晰度与受众意识 |
| 问题解决 | 问题定义、方案设计、验证与迭代 |
| 批判性思维 | 证据判断、假设识别与多角度比较 |
| 反思能力 | 复盘归因、方法总结与迁移应用 |

能力画像同时记录优势、短板、成长历史和最近训练时间。

### 教练式对话

StudentCoach 不模拟面试官。它会先了解学生目标，根据表现继续追问，帮助学生暴露思考过程，并在关键节点提供具体反馈和成长建议。

### 可信评价

大模型负责分析学生表现并给出评价依据，最终能力分、能力变化和成长记录由 Go 服务聚合与保存，避免让模型直接决定长期分数。

### 训练资源与长期记忆

系统通过 RAG 检索适合当前短板的训练案例，通过统一工具注册中心读取学生画像、成长历史和最近评价。Skill 只负责训练策略，不直接访问数据库。

## 一次训练如何进行

1. 学生说明学习目标，或提出通用训练请求。
2. 系统读取学生画像和历史训练记录。
3. 能力分析器形成目标能力标准，学生画像分析器识别当前差距。
4. 训练规划器结合 Skill、RAG 案例和难度生成任务。
5. StudentCoach 组织练习，并通过追问发现能力问题。
6. 能力评价器输出评价依据，Go 服务聚合本轮结果。
7. 系统更新学生能力画像、保存成长记录并生成下一步计划。

## 技术组成

| 层次 | 主要技术与职责 |
|---|---|
| 前端 | React、TypeScript、Vite，提供文字、语音和报告交互 |
| 服务端 | Go，负责业务规则、认证、评价聚合和数据服务 |
| Agent 编排 | Eino Graph，保持既有训练流程拓扑 |
| 训练策略 | 五类学生能力训练 Skill |
| 数据工具 | 统一 Tool Registry，由 Agent 按意图调用 |
| 检索 | Milvus、BM25、融合与重排 |
| 记忆 | Redis 与 MySQL，维护会话和长期能力画像 |
| 实时交互 | WebSocket、流式响应、TTS 与 ASR |
| 扩展能力 | 保留现有 MCP 网页与 GitHub 能力 |
| 自动评测 | mock LLM 驱动的离线 Evaluation Benchmark |

## 快速开始

### 环境要求

- Go 1.26.1，或启用 Go 自动工具链；
- Node.js 20.19 以上或 22.12 以上；
- npm；
- Docker Compose；
- 通义千问 DashScope API Key。

### 启动服务

1. 进入 interview-agent-back-end/interview-agent。
2. 将 .env.example 复制为 .env，并填写 DASHSCOPE_API_KEY。
3. 运行 docker compose up -d，启动 Milvus、Redis 和 MySQL 等依赖。
4. 运行 go run cmd/main.go web，后端默认监听 9090 端口。
5. 进入 interview-agent-web/interview-agent-web。
6. 运行 npm install，再运行 npm run dev。
7. 在浏览器打开 http://localhost:5173。

语音功能默认关闭；需要启用时，请先阅读后端环境变量模板中的安全说明。

## 自动评测

项目提供完全离线的 Evaluation Benchmark，使用 mock LLM 验证核心编排，不依赖真实模型 API 或外部网络。

| 指标 | 样例数 | 当前基线 |
|---|---:|---:|
| Skill Selection Accuracy | 15 | 100.00% |
| Tool Selection Accuracy | 7 | 100.00% |
| Diagnosis Accuracy | 6 | 100.00% |
| Growth Loop Success Rate | 1 | 100.00% |

该基线用于验证确定性的选择、诊断聚合和成长闭环逻辑，不代表真实模型面对开放输入时的泛化准确率。

## 验证项目

- 在后端目录运行 go test ./...，执行完整 Go 测试。
- 在后端目录运行 go test -v ./evaluation，查看可读评测报告。
- 在前端目录运行 npm run build，验证 TypeScript 和生产构建。

## 项目文档

- [后端说明](interview-agent-back-end/interview-agent/README.md)：服务职责、运行环境、配置和验证。
- [前端说明](interview-agent-web/interview-agent-web/README.md)：界面能力、本地开发和后端连接。
- [评测说明](interview-agent-back-end/interview-agent/evaluation/README.md)：评测设计、数据和指标口径。

## 项目状态

项目目前处于持续开发阶段。学生能力训练、长期能力画像、教育 Tool Calling、五类 Skill、RAG、Memory、WebSocket、Speech 和离线评测均已接入。

历史招聘协议仅作为兼容入口保留；新的调用方应使用学生能力训练语义。

## 问题反馈

如需反馈问题或提出改进建议，请使用 [GitHub Issues](https://github.com/haishuiwu/Interview-Agent/issues)。

## 许可

当前仓库尚未附带开源许可证。使用、分发或修改前，请先取得项目维护者授权。
