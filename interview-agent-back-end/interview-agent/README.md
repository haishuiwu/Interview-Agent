# StudentCoach 后端

StudentCoach 后端基于 Go 与 [CloudWeGo Eino](https://github.com/cloudwego/eino) 构建，为学生提供可持续的能力训练。系统从学习目标和学生画像出发，结合 RAG、Memory、Skill 与教育 Tool Calling 完成训练，并在每轮结束后更新长期能力画像。

项目级说明见 [根 README](../../README.md)。

## 设计边界

- 核心领域模型使用 `AbilityStandard`、`StudentProfile`、`LearningDiagnosis`、`StudentAbilityProfile`、`TrainingState` 和 `EvaluationReport`。
- Skill 负责训练策略，不直接访问数据库。
- Tool 负责学生数据能力，并统一从教育 Tool Registry 注册。
- LLM 分析学生表现并提供评价依据；Go Service 聚合最终分数、更新能力画像并保存成长记录。
- Graph 拓扑保持不变，没有引入新的 Graph 或 Multi-Agent 架构。
- 历史招聘字段只存在于请求兼容 DTO、前端 adapter 或存储兼容层，不进入核心运行时模型。

## 执行流程

```text
学习目标与学生画像
  → AbilityAnalyzer
  → StudentProfileAnalyzer
  → QuestionPlanner（画像 + Skill + RAG）
  → StudentCoach（训练 + 追问 + 即时反馈）
  → AbilityEvaluator（评价依据 + Go 确定性聚合）
  → StudentGrowthService（画像更新 + GrowthRecord）
  → GrowthPlanner
```

Eino Graph 仍使用原有节点和边。代码中的部分历史节点 ID、文件名与兼容命令尚未物理改名，但节点内部运行的是学生能力训练语义。

## 核心组件

| 组件 | 责任 |
|---|---|
| `AbilityAnalyzer` | 把学习目标转为能力标准和目标能力 |
| `StudentProfileAnalyzer` | 分析学生现状、优势、短板与学习差距 |
| `QuestionPlanner` | 结合画像、Skill、难度和检索结果规划训练任务 |
| `StudentCoach` | 了解目标、组织训练、针对性追问并反馈 |
| `AbilityEvaluator` | 分析表现、输出评价证据与能力报告 |
| `StudentGrowthService` | 聚合评分、维护画像、保存成长历史并推荐任务 |
| `GrowthPlanner` | 根据本轮结果生成后续成长建议 |

## StudentAbilityProfile

长期能力画像定义在 `internal/model/types.go`：

```go
type StudentAbilityProfile struct {
    StudentID        string
    Summary          string
    AbilityScores    map[string]float64
    Strengths        []string
    Weaknesses       []string
    GrowthHistory    []AbilityGrowthRecord
    LastTrainingTime time.Time
}
```

能力分范围为 0 到 1，固定使用以下维度：

- `logical_thinking`
- `communication`
- `problem_solving`
- `critical_thinking`
- `reflection`

训练结束后的更新链路：

1. StudentCoach 记录作答、命中点、遗漏点和即时反馈。
2. AbilityEvaluator 使用 LLM 生成评价依据。
3. Go 代码根据会话事实聚合分数，不接受 LLM 臆测的最终分。
4. StudentGrowthService 计算训练前后变化并更新 StudentAbilityProfile。
5. 更新结果写入 Memory，并保存可追溯的 GrowthRecord。
6. 下一轮读取画像，优先选择弱项对应 Skill，并影响任务与难度。

## 训练 Skill

| Skill | 主要目标 |
|---|---|
| `logical-thinking` | 识别关系、组织推理链并检查结论 |
| `communication-training` | 清晰、完整、有结构地表达观点 |
| `problem-solving` | 定义问题、形成方案、验证与迭代 |
| `critical-thinking` | 判断证据、识别假设并比较不同解释 |
| `reflection-training` | 复盘表现、归纳原因并迁移改进方法 |

每个 Skill 定义适用场景、训练目标、Agent 行为规则和评价维度。Skill Registry 负责选择与加载，Skill 本身不读写学生数据。

## 教育 Tool Registry

`internal/tool/registry.go` 统一注册 8 个工具：

| Tool | 数据能力 |
|---|---|
| `get_student_profile` | 获取学生画像、能力等级和已知短板 |
| `get_ability_profile` | 获取长期五维能力画像和成长历史 |
| `get_growth_history` | 按能力查询历史训练记录 |
| `get_ability_report` | 获取最近一次能力评价 |
| `search_training_case` | 根据能力短板检索训练案例 |
| `recommend_training_task` | 结合画像、目标和案例推荐任务与 Skill |
| `update_ability_profile` | 把真实评价交给 Go Service 聚合并更新画像 |
| `save_growth_record` | 保存已由评价流程形成的训练结果 |

典型表达训练调用链：

```text
“我想提升自己的表达能力”
  → get_student_profile
  → get_growth_history(communication)
  → search_training_case
  → recommend_training_task
  → communication-training
  → StudentCoach 训练
  → AbilityEvaluator
  → update_ability_profile / save_growth_record
```

学生身份由认证后的运行时上下文注入，模型不能通过 Tool 参数替换当前学生。

## RAG、Memory、MCP 与 Speech

- **RAG**：Milvus 向量召回 + BM25 关键词召回 + RRF 融合 + 可配置重排，用于检索训练案例。
- **Memory**：Redis 提供缓存，MySQL 提供持久化；短期记忆保存对话上下文，长期记忆维护画像与薄弱能力。
- **MCP**：保留网页内容提取和 GitHub 搜索；只有具体训练场景需要时才调用。
- **WebSocket**：`/ws` 承载训练会话和流式消息。
- **Speech**：TTS、实时 ASR 和 HTTP ASR 降级沿用训练链路，默认通过 `SPEECH_ENABLED=false` 关闭。

语音接口：

- `GET /api/speech/capabilities`
- `POST /api/speech/tts`
- `/ws/speech/asr`

浏览器不持有 DashScope API Key。生产环境必须设置强随机 `JWT_SECRET`，并通过 `WEB_ALLOWED_ORIGINS` 配置精确前端 Origin。

## WebSocket 协议

开始训练：

```json
{
  "type": "start_training",
  "learning_goal": "提升结构化表达能力",
  "student_profile": "八年级学生，观点明确，但论据组织不稳定"
}
```

兼容层仍接收以下历史形式，并立即映射为 `learning_goal` 与 `student_profile`：

- `start_training + assessment + profile`
- `start_interview + jd + resume`

新增调用方应只使用新协议。

## Evaluation Benchmark

`evaluation/` 使用确定性的 mock LLM，不访问真实 API、MCP、数据库或外部网络。

```bash
go test -v ./evaluation
```

| 测试 | 指标 | 样例 | 当前基线 |
|---|---|---:|---:|
| `skill_selection_test.go` | Skill Accuracy | 15 | 100.00% |
| `tool_calling_test.go` | Tool Selection Accuracy | 7 | 100.00% |
| `ability_diagnosis_test.go` | Diagnosis Accuracy | 6 | 100.00% |
| `growth_loop_test.go` | Growth Loop Success Rate | 1 | 100.00% |

测试数据位于 `evaluation/testdata/`。29 条基线样例用于验证确定性编排逻辑，不代表真实 LLM 的开放输入准确率。

## 项目结构

```text
interview-agent/
├── cmd/                 # chat、兼容训练、数据加载、Web 与 RAG eval 入口
├── data/
│   ├── questions/       # 学生能力训练案例
│   └── skills/          # Skill 数据
├── evaluation/          # StudentCoach 离线自动评测
├── internal/
│   ├── agent/           # 能力分析、画像分析、教练、评价与成长规划
│   ├── config/          # 环境配置与模型工厂
│   ├── graph/           # 既有 Eino Graph 编排
│   ├── handler/         # HTTP、WebSocket、认证与语音接口
│   ├── loader/          # JSON、PDF、TXT、MD、DOCX 与网页内容加载
│   ├── mcp/             # 现有 MCP 能力
│   ├── memory/          # Redis + MySQL 长短期记忆
│   ├── model/           # 核心领域模型与兼容 DTO
│   ├── rag/             # 向量/BM25 检索、融合、重排和评估
│   ├── service/         # StudentGrowthService 等业务规则
│   ├── skill/           # 五类学生能力训练 Skill
│   ├── speech/          # TTS 与 ASR
│   └── tool/            # 教育 Tool Registry
├── docker-compose.yml
├── Makefile
└── .env.example
```

## 本地运行

要求：

- Go 1.26.1，或设置 `GOTOOLCHAIN=auto`
- Docker Compose
- DashScope API Key
- Node.js（前端及现有网页 MCP 能力需要）

准备配置并启动基础设施：

```bash
cp .env.example .env
# 编辑 .env，至少设置 DASHSCOPE_API_KEY
docker compose up -d
docker compose ps
```

默认端口：

| 服务 | 地址 |
|---|---|
| Web/API | `localhost:9090` |
| Milvus | `localhost:19530` |
| Redis | `localhost:36379` |
| MySQL | `localhost:33306` |
| MinIO API / Console | `localhost:9000` / `localhost:9001` |

启动 Web 后端：

```bash
go run cmd/main.go web
```

健康检查：`GET http://localhost:9090/health`。

其他入口：

```bash
go run cmd/main.go chat
go run cmd/main.go interview      # 历史兼容命令名
go run cmd/main.go load-data      # 加载内置训练数据
go run cmd/main.go load-data FILE # 加载 PDF/TXT/MD 等训练资料
go run cmd/main.go eval -h        # 查看 RAG 离线评估参数
```

前端位于 `../../interview-agent-web/interview-agent-web`：

```bash
cd ../../interview-agent-web/interview-agent-web
npm install
npm run dev
```

## 验证

```bash
go test ./...

cd ../../interview-agent-web/interview-agent-web
npm run build
```

常用 Make 命令：

```bash
make build
make test
make infra-up
make infra-status
make infra-down
```

配置项和语音开关的完整说明见 [`.env.example`](.env.example)。
