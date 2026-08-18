# RAG 离线评估数据集

> 配合 `internal/rag/evaluation_metrics.go` 与 `cmd/eval.go` 使用。
> 整体方案详见 `docs/design/rag_evaluation_plan.md`。

## 1. 目录约定

```
data/eval/
├── README.md                  # 本文件：标注规范
├── manifest.json              # prepare 阶段自动生成的题目清单（含 ID、内容摘要）
├── dataset_v1.sample.json     # 5 条示例样本，对照学怎么标注
├── dataset_v1.json            # 正式数据集（基于 manifest 中的 ID 标注，建议 50-100 条）
└── reports/                   # 评估报告输出目录（由 eval 命令自动创建）
    ├── eval_report_<timestamp>.json
    └── eval_report_<timestamp>.md
```

## 2. 样本结构

每条样本对应 `internal/rag/evaluation_types.go` 中的 `EvalSample`：

```json
{
  "id": "eval_001",
  "query": "Go goroutine 调度模型 GMP",
  "relevant_doc_ids": ["go_basic_001"],
  "topic": "Go 并发",
  "difficulty": "easy",
  "note": "标注说明：什么算相关、什么不算"
}
```

| 字段               | 必填 | 说明                                                                 |
| ------------------ | ---- | -------------------------------------------------------------------- |
| `id`               | 是   | 样本唯一 ID，建议 `eval_001` 这种带零填充的序号                      |
| `query`            | 是   | 模拟 Phase 1 输出的 SearchQuery（**不是用户原始提问**，详见 §3）     |
| `relevant_doc_ids` | 是   | 人工标注的相关题目 ID 列表（黄金标准），ID 来自 `data/questions/`    |
| `topic`            | 否   | 技术领域，如 `"Go 并发"` / `"MySQL"` / `"分布式系统"` / `"微服务"`   |
| `difficulty`       | 否   | `easy` / `medium` / `hard`，便于分组分析                             |
| `note`             | 否   | 标注理由，强烈建议填写（半年后回看时救命）                           |

## 3. Query 怎么写：模拟 Phase 1 的 SearchQuery

InterviewAgent 的 RAG 流程是**两阶段问题规划**：

```
用户简历/JD ─→ Phase 1 LLM 规划方向 + 生成 SearchQuery ─→ Phase 2 RAG 检索
                                                              ↑
                                                  评估的就是这一步
```

**Query 必须模拟 Phase 1 的输出**，而不是用户的原始提问。Phase 1 输出特点：

- 是**关键词组合**而非完整问句（如 `"Go goroutine 调度模型 GMP"`）
- 包含**技术名词**和**子领域**（如 `"MySQL InnoDB 索引 B+树 聚簇索引"`）
- 长度通常 10-30 个汉字 + 几个英文术语

不要写成：

- ❌ `"什么是 goroutine？"`（用户原始提问风格）
- ❌ `"Go"`（太宽泛）
- ❌ `"请详细解释 Go 语言中 goroutine 的调度模型，包括 GMP 三个组件的协作机制"`（太长太完整）

要写成：

- ✅ `"Go goroutine 调度模型 GMP"`
- ✅ `"MySQL 事务 ACID 原理 MVCC 实现"`
- ✅ `"分布式系统 CAP 定理 BASE 一致性"`

如果你想知道 Phase 1 实际产出什么样的 SearchQuery，可以跑一次 `interview` 命令，
观察 `internal/graph/orchestrator.go` 阶段 1 的日志输出，模仿它的风格。

## 4. 相关性怎么判定

只要满足以下**任一**条件，就标 `relevant`：

1. **题目本身就是 query 想问的**（最常见）
   - query: `"MySQL 事务 ACID MVCC 实现"`
   - relevant: `mysql_002`（题目内容就是 ACID 和 MVCC）

2. **题目的 follow-up 涵盖 query**
   - query: `"MySQL 间隙锁 幻读"`
   - relevant: `mysql_002`（follow-up 包含 "幻读在 RR 隔离级别下是怎么解决的"）

3. **题目的参考答案完整回答了 query**
   - query: `"MySQL 半同步复制"`
   - relevant: `mysql_004`（reference 涵盖了半同步复制）

**不要**因为下面这些理由就标 relevant：

- 题目跟 query 是同一个大领域（同样是 MySQL 但讲索引 vs 讲事务，不算）
- 题目偶尔提到 query 里的某个词（如 mysql_001 提到 "InnoDB" 但讲的是索引）
- 题目跟 query "有点关系"（如 `"Go 内存模型"` 和 `"Go GC"`，不算）

宁可标得严一些。评估的目的是发现**真正的检索失败**，标得太宽会让指标虚高。

## 5. 数据集规模建议

| 规模    | 用途                                | 标注成本     |
| ------- | ----------------------------------- | ------------ |
| 5-10 条 | 跑通流程 / smoke test               | 30 分钟      |
| 20-30 条| MVP 评估，能反映明显问题            | 2 小时       |
| 50-100 条| 上线前 baseline，能做 A/B 对比     | 半天-一天    |
| 200+ 条 | 长期监控，需要按 topic 分层抽样     | 1-2 天       |

建议起步规模：**50 条左右**，覆盖 4 大领域（Go / MySQL / 微服务 / 分布式），
每个领域 10-15 条，难度分布按 easy:medium:hard ≈ 3:5:2。

## 6. 快速上手：从零跑通 RAG 离线评估

### 前置条件

在跑评估之前，确保以下服务和配置已就绪：

**1. Docker Desktop 已启动**

评估依赖 Milvus 向量数据库，Milvus 跑在 Docker 中。

```bash
# 检查 Docker 是否正在运行
docker ps

# 如果报错 "Cannot connect to the Docker daemon"，需要先启动 Docker Desktop
```

**2. Milvus 服务已启动**

```bash
# 项目根目录下的 docker-compose 会启动 Milvus
docker compose up -d

# 验证 Milvus 是否就绪（默认端口 19530）
docker ps | grep milvus
```

**3. LLM API Key 已配置**

评估的 prepare 阶段需要调用 LLM 解析题库。确保 `config.yaml`（或环境变量）中配置了正确的 LLM API Key。

```bash
# 检查配置文件
cat config.yaml | grep api_key
```

**4. Go 环境**

```bash
go version  # 需要 Go 1.21+
```

### Step 1：准备评估环境

将 `data/questions/` 下的 MD 题库文件通过 LLM 解析为结构化题目，写入 Milvus + BM25 索引，同时导出题目清单 `manifest.json`。

```bash
go run ./cmd/ eval --prepare
```

**预计耗时**：5-15 分钟（取决于 LLM API 响应速度，5 个题库文件约 200+ 道题）。

**成功标志**：

```
[Prepare] ✓ distributed_interview.md: 40 道题写入 Milvus
[Prepare] ✓ go_interview.md: 82 道题写入 Milvus
[Prepare] ✓ mq_interview.md: 45 道题写入 Milvus
[Prepare] ✓ mysql_interview.md: 38 道题写入 Milvus
[Prepare] ✓ redis_interview.md: 48 道题写入 Milvus
[Prepare] manifest 已写入: data/eval/manifest.json（共 253 条）
```

**常见问题**：
- 卡在"找到 5 个 MD 题库文件"不动 → Docker/Milvus 没启动，检查 `docker ps`
- 某段解析失败 → 通常是 LLM API 超时，会自动跳过该段继续，不影响其他题目
- 如果题目数量明显偏少，可以重新跑一次（prepare 会先清除旧数据再重新写入）

### Step 2：自动生成评估数据集

基于 manifest 自动采样 50 条评估样本，自动标注相关题目。

```bash
go run ./cmd/ eval --gen-dataset
```

**预计耗时**：几秒钟（纯本地计算，不调用 LLM）。

**成功标志**：生成 `data/eval/dataset_v1.json`，包含 50 条样本，覆盖 5 个 topic。

### Step 3：执行评估，生成报告

```bash
go run ./cmd/ eval \
    -dataset data/eval/dataset_v1.json \
    -out data/eval/reports \
    -note "baseline"
```

**预计耗时**：约 1 分钟（50 条样本，每条需要调用 Milvus 检索 + BM25 检索 + LLM Rerank）。

**成功标志**：在 `data/eval/reports/` 下生成两个文件：

```
data/eval/reports/
├── eval_report_20260513_170013.json   # 程序可读的完整数据
└── eval_report_20260513_170013.md     # 人类可读的 Markdown 报告
```

打开 `.md` 报告查看结果：

```bash
cat data/eval/reports/eval_report_*.md
```

报告包含：整体 Recall@10/MRR 指标、按 Topic 分组对比、按难度分组对比、Worst-10 异常样本列表。

### 完整流程一键执行

如果想一次跑完全部步骤：

```bash
# 1. 确保 Docker 和 Milvus 已启动
docker compose up -d

# 2. 三步走
go run ./cmd/ eval --prepare          # 解析题库 → Milvus/BM25
go run ./cmd/ eval --gen-dataset      # 自动生成评估数据集
go run ./cmd/ eval -note "baseline"   # 执行评估 → 生成报告

# 3. 查看报告
cat data/eval/reports/eval_report_*.md
```

## 7. 怎么做 A/B 对比

A/B 对比的核心思路：**在同一份数据集上，改一个参数，跑两次评估，对比两份报告**。

```bash
# A: 基线（开 Rerank）
go run ./cmd/ eval -note "with-rerank"

# B: 关闭 Rerank
go run ./cmd/ eval -skip-rerank -note "no-rerank"
```

两份报告的 `Config` 字段会记录当时的参数差异，`Overall` 和 `WorstSamples` 字段
可以直观看出哪些 query 在 Rerank 之后排名变好/变差。

如果想调整 TopK 等参数做更多实验，需要修改 `cmd/eval.go` 中 `ragCfg` 的配置值后重新跑。
