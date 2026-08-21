# StudentCoach：学生能力提升 Agent

StudentCoach 是一个面向长期成长的学生能力训练系统。它会结合学习目标、学生画像与历史训练记录选择训练方式，通过任务、追问和反馈发现能力问题，并在训练结束后持续更新学生能力画像。

系统聚焦训练与成长，不替代教师评价，也不直接给出升学、招聘或录用结论。

## 核心能力

- **Eino Graph**：保持既有拓扑，串联能力标准分析、学生画像分析、训练规划、训练对话、评价和成长规划。
- **StudentCoach**：以 AI 能力训练教练的方式了解目标、选择训练方式、追问关键问题并给出成长建议。
- **五类训练 Skill**：逻辑思维、表达训练、问题解决、批判性思维和反思训练。
- **教育 Tool Calling**：通过统一 Registry 获取能力画像、成长历史和评价报告，检索案例、推荐任务并保存成长结果。
- **长期能力画像**：Memory 跨轮维护五维能力分、优势、短板、成长历史和最近训练时间。
- **RAG**：Milvus 向量检索与 BM25 关键词检索结合，为训练任务提供案例。
- **实时交互**：保留 WebSocket、流式响应、TTS 和 ASR；语音能力默认关闭。
- **MCP**：保留现有网页与 GitHub 能力，按具体训练场景使用。
- **离线评测**：使用 mock LLM 验证 Skill 选择、Tool 调用、能力诊断和成长闭环，不依赖真实模型 API。

## 能力维度

StudentCoach 当前持续跟踪以下五个维度，分数范围为 0 到 1：

| 字段 | 能力 |
|---|---|
| `logical_thinking` | 逻辑思维 |
| `communication` | 沟通与结构化表达 |
| `problem_solving` | 问题分析与解决 |
| `critical_thinking` | 证据判断与批判性思维 |
| `reflection` | 复盘与迁移 |

## 训练与成长闭环

```text
学习目标
  → AbilityAnalyzer：形成能力标准
  → StudentProfileAnalyzer：分析学生画像与学习差距
  → QuestionPlanner：结合画像、Skill 与 RAG 规划任务
  → StudentCoach：训练、追问与即时反馈
  → AbilityEvaluator：生成评价依据，Go 聚合最终分数
  → StudentGrowthService：更新 StudentAbilityProfile 并保存 GrowthRecord
  → GrowthPlanner：生成下一步成长计划
```

下一次训练开始时，StudentCoach 会读取已有能力画像。若学生只说“帮我训练一下”，系统会优先选择当前较弱的能力，而不是随机选择 Skill；画像也会影响任务推荐和难度。

大模型只负责分析表现和提供评价依据。能力分聚合、画像更新和成长记录保存由 Go Service 控制，LLM 不直接决定最终分数。

## 学生能力画像

```json
{
  "student_id": "student_001",
  "summary": "逻辑基础稳定，需要加强结构化表达和方案验证。",
  "ability_scores": {
    "logical_thinking": 0.78,
    "communication": 0.65,
    "problem_solving": 0.55,
    "critical_thinking": 0.61,
    "reflection": 0.58
  },
  "strengths": ["能识别主要因果关系"],
  "weaknesses": ["缺少结构化表达", "验证方案意识不足"],
  "growth_history": [],
  "last_training_time": "2026-08-21T10:00:00+08:00"
}
```

## Skill 与 Tool

内置训练 Skill：

- `logical-thinking`
- `communication-training`
- `problem-solving`
- `critical-thinking`
- `reflection-training`

统一 Tool Registry 注册：

- `get_student_profile`
- `get_ability_profile`
- `get_growth_history`
- `get_ability_report`
- `search_training_case`
- `recommend_training_task`
- `update_ability_profile`
- `save_growth_record`

Skill 只定义训练策略，不直接访问数据库；所有数据能力通过 Tool 调用，成长与评价业务规则仍由 `StudentGrowthService` 等 Go Service 执行。

## Evaluation Benchmark

`interview-agent-back-end/interview-agent/evaluation/` 提供完全离线的自动评测：

| 指标 | 样例 | 当前基线 |
|---|---:|---:|
| Skill Accuracy | 15 | 100.00% |
| Tool Selection Accuracy | 7 | 100.00% |
| Diagnosis Accuracy | 6 | 100.00% |
| Growth Loop Success Rate | 1 | 100.00% |

合计 29 条确定性样例。该结果验证编排、选择、诊断聚合和成长闭环逻辑，不代表真实模型在开放输入上的泛化准确率。

## 快速开始

环境要求：

- Go 1.26.1，或启用 `GOTOOLCHAIN=auto`
- Node.js 与 npm
- Docker Compose
- 通义千问 DashScope API Key

启动后端与依赖：

```bash
cd interview-agent-back-end/interview-agent
cp .env.example .env
# 在 .env 中设置 DASHSCOPE_API_KEY
docker compose up -d
go run cmd/main.go web
```

后端监听 `http://localhost:9090`，健康检查为 `GET /health`，训练 WebSocket 为 `/ws`。

启动前端：

```bash
cd interview-agent-web/interview-agent-web
npm install
npm run dev
```

前端默认访问地址为 `http://localhost:5173`。

可选命令：

```bash
go run cmd/main.go chat
go run cmd/main.go load-data
go run cmd/main.go eval -h
```

## WebSocket 训练协议

新请求只使用学生能力训练语义：

```json
{
  "type": "start_training",
  "learning_goal": "提升课堂讨论中的结构化表达能力",
  "student_profile": "八年级学生，能提出观点，但论据组织不稳定"
}
```

旧的 `start_interview`、`jd`、`resume`、`assessment` 和 `profile` 仅保留在协议兼容 DTO 与前端映射层，进入核心运行时前会转换为新字段；核心领域模型不继续消费招聘字段。

## 测试

```bash
cd interview-agent-back-end/interview-agent
go test ./...
go test -v ./evaluation

cd ../../interview-agent-web/interview-agent-web
npm run build
```

## 目录

- `interview-agent-back-end/interview-agent`：Go 后端、Eino Graph、Agent、Skill、Tool、RAG、Memory、Speech 与 Evaluation。
- `interview-agent-web/interview-agent-web`：React + TypeScript 学生训练界面。

后端的配置、协议和实现边界详见 [后端 README](interview-agent-back-end/interview-agent/README.md)。
