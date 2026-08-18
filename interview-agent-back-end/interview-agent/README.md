# 师练 AI - 教师教学能力训练系统（后端）

基于 Go + [Eino](https://github.com/cloudwego/eino) 构建，面向教师资格结构化问答、教师招聘试讲答辩和青年教师发展。系统以教学能力诊断与持续训练为目标，不输出录用结论。完整产品立意与量化口径见 [项目级 README](../../README.md)。

> 为兼容历史客户端与存储，代码中的 `JD`、`Resume`、`InterviewState` 等类型名暂时保留；对外协议和业务语义已经迁移为目标标准、教学档案与能力训练。

## 系统架构

```
用户交互层（CLI）
       ↓
Agent 编排层（Eino Graph DAG）
  ┌─────────────────────────────────────────────┐
  │  标准解析 → 档案诊断 → 教师题库检索 → 训练规划  │
  │                                   ↓          │
  │              AI教研员（启发追问+动态难度调节）  │
  │                                   ↓          │
  │                         形成性评价 → 提升计划  │
  └─────────────────────────────────────────────┘
       ↓
基础能力层
  ┌─────────────┬─────────────┬──────────────┐
  │ RAG 多路召回  │ 记忆系统      │ MCP 工具      │
  │ Milvus+BM25  │ Redis+MySQL  │ GitHub/Web   │
  │ RRF+Rerank   │ 短期+长期     │              │
  └─────────────┴─────────────┴──────────────┘
       ↓
基础设施层（Docker Compose 一键启动）
  ┌───────────┬───────────┬───────────┐
  │  Milvus   │   Redis   │   MySQL   │
  │ 向量数据库  │  会话缓存   │  持久化    │
  └───────────┴───────────┴───────────┘
```

## 核心特性

- **多 Agent 协作**：目标标准解析、教学档案诊断、训练规划、AI 教研员、形成性评价与提升计划通过 DAG 编排协作
- **RAG 多路召回**：Milvus 向量检索 + BM25 关键词检索 + RRF 融合 + LLM Rerank 重排序
- **RAG 质量评估**：Faithfulness / Relevance / Completeness 三维评估
- **动态难度调节**：根据作答表现实时调整题目难度（连续答对升难度，连续答错降难度）
- **Agent 记忆系统**：短期对话记忆（滑动窗口） + 长期用户画像（薄弱点追踪），Redis 缓存 + MySQL 持久化
- **场景化题库**：教育理论题使用 Milvus + BM25 检索，教学实践和课堂情境依据真实档案动态生成
- **MCP 协议集成**：支持网页提取教师岗位/考核标准；GitHub 仅在数字化教学能力提升时按需搜索
- **流式与语音交互**：AI 教研员提问支持流式响应、TTS、实时 ASR 与 HTTP ASR 降级
- **训练记录持久化**：评估报告、提升计划和历史薄弱能力写入 MySQL/Redis，支持后续针对性复测

## 语音面试（默认关闭）

语音能力是现有文字面试的可选增强，不改变出题、评分、追问和报告链路：

- 面试官问题通过 `POST /api/speech/tts` 合成为 WAV，并由浏览器播放。
- 面试者通过独立的 `/ws/speech/asr` 上传 16 kHz、mono、signed 16-bit little-endian PCM。
- 实时 ASR 连接失败、传输断开或 final 超时时，Go 后端使用同一有界 PCM 缓冲调用 HTTP ASR。
- ASR final 只写入输入框草稿，不会自动提交答案；用户仍需检查并点击发送。
- 浏览器不持有 DashScope API Key，不直接连接供应商，也不保存整段录音。

安全默认配置：

```env
# 安全默认：仅文字面试
SPEECH_ENABLED=false

# 第一批灰度：只开启面试官朗读
SPEECH_ENABLED=true
SPEECH_TTS_ENABLED=true
SPEECH_ASR_ENABLED=false

# 全量语音能力
SPEECH_ENABLED=true
SPEECH_TTS_ENABLED=true
SPEECH_ASR_ENABLED=true
```

生产环境还必须设置：

```env
# 复用现有 DashScope Key，不要提交到源码
DASHSCOPE_API_KEY=...

# 必须是实际前端 Origin；多域名用英文逗号分隔，不支持通配符
WEB_ALLOWED_ORIGINS=https://interview.example.com

# 必须替换默认值；同时用于 JWT 签名和语音日志 user_id 的 HMAC 化
JWT_SECRET=使用随机高强度密钥
```

所有语音输入、响应、并发和上游调用都有硬上限，完整参数见 [`.env.example`](.env.example)。全局回滚只需设置 `SPEECH_ENABLED=false` 并重启 Go 服务；也可通过 `SPEECH_TTS_ENABLED`、`SPEECH_ASR_ENABLED` 独立关闭。

语音生命周期以 JSON Lines 写入标准输出，事件包括 `speech_tts_*`、`speech_asr_*`、`speech_session_cancelled` 和 `speech_limit_rejected`。日志只包含 HMAC 用户标识、request/question/provider ID、模型、耗时、字节数、字符数、降级状态和稳定错误码；不得加入 JWT、API Key、签名 URL、音频、待朗读全文或完整转写。

发布前按[语音面试发布检查表](../../docs/voice-interview-release-checklist.md)执行自动验证、故障注入、灰度观察和回滚演练。完整设计和阶段记录见[语音面试实施方案](../../docs/voice-interview-implementation-plan.md)。

## 项目结构

```
interview-agent/
├── cmd/main.go                        # 程序入口（CLI）
├── internal/
│   ├── agent/                         # 7 个 Agent 实现
│   │   ├── chat_agent.go              #   聊天 Agent（日常对话/面试引导）
│   │   ├── jd_analyzer.go             #   JD 分析
│   │   ├── resume_matcher.go          #   简历匹配
│   │   ├── question_planner.go        #   出题规划 + 动态难度调节
│   │   ├── interviewer.go             #   面试官（提问/评分/追问/流式输出）
│   │   ├── evaluator.go              #   评估报告生成
│   │   └── review_planner.go         #   复习计划生成
│   ├── rag/                           # RAG 多路召回
│   │   ├── embedding.go               #   DashScope Embedding（text-embedding-v3）
│   │   ├── retriever_vector.go        #   Milvus 向量检索（Eino 官方组件）
│   │   ├── retriever_bm25.go         #   BM25 关键词检索
│   │   ├── fusion.go                  #   RRF 多路融合算法
│   │   ├── rerank.go                  #   LLM Rerank 重排序
│   │   └── evaluation.go             #   RAG 质量评估（三维指标）
│   ├── memory/                        # 记忆系统
│   │   ├── short_term.go             #   短期记忆（对话上下文滑动窗口）
│   │   ├── long_term.go              #   长期记忆（用户画像/薄弱点追踪）
│   │   ├── store.go                  #   存储接口 + Redis 实现
│   │   ├── mysql_store.go            #   MySQL 持久化（自动建表）
│   │   └── combined_store.go         #   组合存储（Redis缓存+MySQL持久化）
│   ├── mcp/                           # MCP 协议工具集成
│   │   ├── github_tool.go            #   GitHub 搜索工具（MCP）
│   │   └── web_scraper.go            #   网页抓取（Playwright MCP Server + stdio）
│   ├── graph/
│   │   └── orchestrator.go           # Graph DAG 全局编排（串联7个Agent+RAG+记忆）
│   ├── model/
│   │   └── types.go                  # 数据模型定义
│   ├── loader/                        # 文档加载
│   │   ├── document.go               #   统一入口（自动识别格式）
│   │   ├── pdf.go                    #   PDF 解析
│   │   ├── docx.go                   #   DOCX 解析
│   │   └── web.go                    #   MCP 网页抓取 + LLM JD 提取
│   └── config/
│       ├── config.go                 # 配置管理（环境变量）
│       └── llm.go                    # ChatModel 工厂（DashScope OpenAI兼容）
├── data/questions/                    # 面试题库（JSON格式，共 22 道）
│   ├── golang.json                    #   Go 语言（6题）
│   ├── distributed_system.json        #   分布式系统（6题）
│   ├── mysql.json                     #   MySQL（5题）
│   └── microservice.json              #   微服务（5题）
├── docker-compose.yml                 # 基础设施编排（Milvus+Redis+MySQL）
├── Makefile                           # 常用命令
└── .env.example                       # 环境变量模板
```

---

## 详细运行指南

### 第一步：环境准备

你需要以下工具，逐个确认：

**1) Go 1.26.1（或启用 `GOTOOLCHAIN=auto`）**

```bash
# 检查是否已安装
go version

# 如果没有，macOS 用 Homebrew 安装：
brew install go

# 或从官网下载安装包：https://go.dev/dl/
```

**2) Node.js 18+（MCP 网页抓取依赖）**

```bash
# 检查是否已安装
node --version
npx --version

# 如果没有，macOS 用 Homebrew 安装：
brew install node

# 或从官网下载安装包：https://nodejs.org/
```

> Node.js 用于运行 Playwright MCP Server，支持 JS 渲染的网页抓取（招聘页面 JD 提取）。
> 首次运行时 npx 会自动下载 `@playwright/mcp` 包，无需手动安装。

**3) Docker Desktop**

```bash
# 检查是否已安装
docker --version
docker compose version

# 如果没有，下载安装：https://www.docker.com/products/docker-desktop/
# 安装后确保 Docker Desktop 已启动（状态栏有鲸鱼图标）
```

**4) 通义千问 API Key**

1. 打开 https://dashscope.console.aliyun.com/
2. 用支付宝/淘宝账号登录（个人即可，无需企业认证）
3. 点击左侧「API-KEY 管理」→「创建新的 API-KEY」
4. 复制生成的 Key（以 `sk-` 开头）

> 新用户有免费额度，qwen-plus 模型足够跑完多次完整面试。

### 第二步：克隆项目

```bash
git clone <项目下载地址>
cd interview-agent
```

### 第三步：配置环境变量

```bash
cp .env.example .env
```

用任意编辑器打开 `.env`，**只需改第一行**，把 API Key 填进去：

```
DASHSCOPE_API_KEY=sk-你的真实key粘贴在这里
```

其余配置项（Milvus/Redis/MySQL 地址）保持默认，和 docker-compose.yml 中的端口一一对应。

### 第四步：启动基础设施

```bash
# 启动 Milvus（向量数据库）+ Redis（缓存）+ MySQL（持久化）
make infra-up

# 首次启动需要拉取 Docker 镜像，耐心等 3~5 分钟
# Milvus 依赖 etcd + minio，共 5 个容器
```

等待启动完成后，验证所有服务是否正常：

```bash
make infra-status
```

你应该看到 5 个容器全部 running：

```
NAME                    STATUS
interview-agent-etcd    Up (healthy)
interview-agent-minio   Up (healthy)
interview-agent-milvus  Up (healthy)
interview-agent-redis   Up (healthy)
interview-agent-mysql   Up (healthy)
```

如果某个服务不是 healthy，等 30 秒再查一次（Milvus 启动较慢）。

### 第五步：安装 Go 依赖

```bash
go mod tidy
```

首次执行会下载所有依赖包（Eino框架、Milvus SDK、Redis客户端等），耗时 1-2 分钟。

验证编译通过：

```bash
go build ./...
# 没有任何输出 = 编译成功
```

### 第六步：启动后端服务

```bash
go run cmd/main.go web
```

启动时会依次连接所有基础设施：

```
=== InterviewAgent - AI 模拟面试系统 ===

模型: qwen-plus
[MCP] Playwright Web Scraper 就绪
[MySQL] 已连接
[MySQL] 表结构就绪
[Milvus] 已连接: localhost:19530
[Milvus] Indexer 就绪，集合: interview_questions
[Milvus] Retriever 就绪，TopK: 10
[Auth] 用户表就绪

[Web] 服务器启动: http://localhost:9090
[Web] 前端请访问: http://localhost:5173
```

> 如果 Milvus 初始化卡住或报 `collection 加载超时`，请参见下方「常见问题」中的解决方案。

### 第七步：启动前端

前端项目 `interview-agent-web`：

```bash
cd ../interview-agent-web
npm install        # 首次运行需安装依赖
npm run dev        # 启动开发服务器
```

启动后访问 http://localhost:5173 即可使用。

### 第八步：使用面试系统

1. **注册/登录**：在页面上注册账号并登录
2. **上传题库**（可选）：上传 PDF/TXT/MD 格式的面试题库文件，系统自动解析并向量化存入 Milvus。不上传也可以面试，系统会由 LLM 直接出题
3. **开始面试**：输入 JD 和简历，系统自动执行 JD 分析 → 简历匹配 → RAG 检索 → 出题规划 → 逐题面试 → 评估报告 → 复习规划 全流程。JD 支持三种输入方式：
   - **URL 抓取**：粘贴招聘页面链接，通过 Playwright MCP 自动抓取并提取 JD 正文
   - **文件上传**：支持 PDF/TXT/DOCX/MD 格式
   - **手动粘贴**：直接粘贴 JD 文本

   > **关于 URL 抓取**：大多数主流招聘网站（Boss 直聘、拉勾、猎聘等）有登录墙和反爬机制，即使通过 Playwright 渲染也会被拦截，导致无法自动抓取。遇到这种情况请改用文件上传或手动粘贴方式。以下是经过验证可正常抓取的测试链接：
   > ```
   > https://hewa.cn/positionDetails/F0kSXTx79mu6WjmQ1et2Dg_hw2_.html
   > ```
4. **查看历史**：历次面试的评估报告和复习计划均持久化在 MySQL 中，可随时查看

> 系统也支持 CLI 模式：`go run cmd/main.go` 进入聊天模式，`go run cmd/main.go interview` 直接启动命令行面试。

---

## 常见问题

**Q: 启动时卡在 `[Milvus] 已连接` 不动，或报 `collection 加载超时`？**

这是 Milvus standalone 的已知问题（从 2.4 到 2.6 均存在）：Milvus 重启后内部节点 ID 会变更，但旧的 collection 加载元数据没有同步清理，导致 `LoadCollection` 永久卡住。执行以下命令清空 Milvus 数据后重启即可恢复：

```bash
docker compose down
docker volume rm interview-agent_milvus_data
docker compose up -d
```

> ⚠️ 这会清空 Milvus 中已上传的题库数据，重启后需要重新上传题库（通过前端上传或执行 `go run cmd/main.go load-data`）。MySQL 和 Redis 的数据不受影响。

**Q: `make infra-up` 后容器一直不 healthy？**

Milvus 首次启动较慢（需要初始化存储），等 1-2 分钟。如果持续不健康：
```bash
docker compose logs milvus   # 查看 Milvus 日志
docker compose logs mysql    # 查看 MySQL 日志
```

**Q: `连接 Milvus 失败` / `连接 Redis 失败` / `连接 MySQL 失败`？**

确认 Docker 容器在运行：`make infra-status`。确认 `.env` 中的地址和 `docker-compose.yml` 端口映射一致（默认 Milvus 19530、Redis 6379、MySQL 3306）。

**Q: `config: DASHSCOPE_API_KEY is required`？**

`.env` 文件不存在或 Key 未填。执行 `cp .env.example .env` 后填入真实 Key。

**Q: 大模型返回解析失败（`parse response` 错误）？**

偶发的 JSON 格式异常，重试即可。频繁出现可在 `.env` 中换更强的模型：`LLM_MODEL=qwen-max`。

**Q: 端口被占用（如 3306）？**

本地已有 MySQL/Redis 实例。修改 `docker-compose.yml` 中的端口映射，同步修改 `.env` 中对应地址。

---

## 技术栈

| 类别 | 选型 | 用途 |
|------|------|------|
| 语言 | Go 1.26.1 | 主语言 |
| AI 框架 | [Eino](https://github.com/cloudwego/eino)（CloudWeGo） | Agent 编排 / 工具调用 / RAG |
| 大模型 | 通义千问 DashScope | LLM 推理（qwen-plus / qwen-max） |
| Embedding | text-embedding-v3 | 文本向量化（1024维） |
| 向量数据库 | Milvus 2.4 | 向量存储与 COSINE 检索 |
| 缓存 | Redis 7 | 会话缓存 / 用户画像缓存 |
| 持久化 | MySQL 8.0 | 用户画像 / 面试历史 / 评估报告 |
| 容器化 | Docker Compose | 一键部署基础设施 |

## 常用命令

```bash
make run           # 运行项目
make build         # 编译到 bin/interview-agent
make test          # 运行测试
make infra-up      # 启动 Milvus + Redis + MySQL
make infra-down    # 停止基础设施
make infra-status  # 查看容器状态
```

## 面试流程示意

```
输入 JD + 简历
      │
      ▼
┌─────────────┐    ┌──────────────┐
│  JD 分析     │───▶│  简历匹配     │
│  Agent       │    │  Agent       │
└─────────────┘    └──────────────┘
                          │
                          ▼
                   ┌──────────────┐
                   │  RAG 多路召回  │ ◀─── Milvus 向量检索
                   │  RRF + Rerank │ ◀─── BM25 关键词检索
                   └──────┬───────┘
                          │
                          ▼
                   ┌──────────────┐
                   │  出题规划     │
                   │  Agent       │
                   └──────┬───────┘
                          │
                          ▼
                   ┌─────────────┐
                   │  面试官       │◀─── 动态难度调节
                   │  Agent       │◀─── 短期记忆（对话窗口）
                   │  (多轮对话)   │
                   └──────┬──────┘
                          │
            ┌─────────────┴──────────────┐
            ▼                            ▼
     ┌─────────────┐          ┌──────────────┐
     │  评估报告    │          │  复习规划     │
     │  Agent       │          │  Agent       │
     └──────┬──────┘          └──────┬───────┘
            │                        │
            ▼                        ▼
     ┌──────────────────────────────────────┐
     │  持久化：MySQL 面试记录 + Redis 缓存   │
     │  长期记忆：用户画像 + 薄弱点追踪       │
     └──────────────────────────────────────┘
```
