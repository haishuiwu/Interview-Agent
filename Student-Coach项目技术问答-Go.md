# Student-Coach 项目技术问答（Go / Eino）

> **项目定位**：Student-Coach｜AI 自适应学习训练与知识掌握诊断系统。

> **核心问题**：如何根据学生的真实作答判断具体知识点哪里不会，并动态决定下一步学什么、练什么、以什么难度练。

> **核心闭环**：学习状态分析 → 训练规划 → 题目检索 → 作答评价 → 动态追问 → 掌握度更新 → 下一轮训练。

> **可信边界**：LLM 负责语义理解、内容生成和作答证据提取；Go 与 Eino Graph 负责状态机、追问次数、难度策略、证据聚合、权限和状态写入。

---

## 一、项目定位与业务闭环

### 1. 如何用一分钟介绍 Student-Coach？

Student-Coach 是面向具体学科、教材章节和知识点的自适应学习训练系统。系统先分析学习目标和历史学习状态，再按知识点、章节、难度和薄弱点检索训练题，通过 WebSocket 等待学生真实作答。

Evaluator 会提取正确点、错误点和遗漏点。系统根据证据决定是否进行一次引导式追问，再由 Go 聚合主回答和追问表现、更新知识点掌握度、调整下一题难度，并在训练结束后生成学习诊断和下一轮学习路径。

它不是通用聊天机器人，也不是普通题库。核心差异是：每次真实作答都会改变系统对学生当前掌握状态的判断，并影响下一步教学行为。

### 2. 最终业务流程是什么？

```text
学习目标分析
→ 学习状态分析
→ 训练规划
→ 题目检索
→ Coach 提问
→ WebSocket 等待真实作答
→ 作答评价
→ 是否追问
→ 证据聚合
→ 动态难度调整
→ 知识掌握度更新
→ 下一题 / 针对性复习
→ 学习报告
→ 下一轮学习路径
```

逐题循环位于 Graph 的 `adaptive_training` 节点内部。Graph 不会预先生成或模拟学生回答。

### 3. Student-Coach 与通用聊天助手有什么区别？

通用聊天助手主要根据当前输入生成回复，通常没有稳定的学科结构、掌握状态和受控训练流程。

Student-Coach 维护明确的领域状态：

- 学科、教材、章节和知识点；
- 当前题目、实际难度和作答证据；
- 已掌握内容、薄弱知识点和近期表现；
- 提示依赖、追问次数和训练阶段；
- 跨训练保存的知识掌握画像与学习轨迹。

因此它不是“更会聊天”，而是将 LLM 放入确定性教学工作流中。

### 4. 项目的技术亮点和边界是什么？

主要亮点：

- Eino Graph 编排确定性学习流程；
- WebSocket Human-in-the-loop，等待真实学生输入；
- 基于作答证据的受控追问和掌握度更新；
- 最近表现、当前掌握度和训练阶段共同参与难度调整；
- Milvus + BM25 为当前状态选择训练题；
- Session State 与长期 Learning Memory 分层；
- Skill、Tool、MCP 和 ASR/TTS 保持清晰边界。

明确边界：

- 当前是 Graph 编排的单 Agent 工作流，不是动态 Multi-Agent；
- 在线 RAG 没有调用仓库中的 RRF 实现；
- Eino `shortTermMem` 尚未进入核心 Graph；
- Skill Runtime 与完整 Graph 是两个独立入口；
- 系统提供形成性反馈，不替代正式考试、教师评价或高风险决策。

---

## 二、Eino Graph 与确定性工作流

### 5. 为什么使用 Eino Graph？

完整训练存在稳定依赖关系。必须先分析目标和学习状态，才能规划训练；必须获得真实回答，才能评价、追问和更新掌握度。

如果把全部控制权交给一个 ReAct Agent，模型可能跳过状态读取、重复追问、在证据不足时更新状态，或者无法稳定终止。

Eino Graph 用于固定关键顺序和分支。LLM 可以负责开放语义任务，但流程推进、次数限制、状态写入和终止条件由 Go 控制。

### 6. 当前 Graph 的真实节点是什么？

```text
learning_goal_analysis
→ learner_state_analysis
→ training_planning
→ adaptive_training
→ targeted_review
→ session_summary
→ next_training_plan
```

其中：

- `learning_goal_analysis`：提取学科、教材、章节和目标知识点；
- `learner_state_analysis`：结合输入和长期记忆分析当前学习状态；
- `training_planning`：规划方向、难度和检索条件；
- `adaptive_training`：逐题检索、提问、等待、评价、追问、调难度和更新状态；
- `targeted_review`：针对已有错误证据提供巩固；
- `session_summary`：生成知识掌握诊断；
- `next_training_plan`：生成下一轮学习路径。

### 7. LLM 与 Go 分别负责什么？

LLM 负责：

- 理解学习目标和学生描述；
- 生成题目、提示和苏格拉底式追问；
- 提取回答中的正确点、错误点和遗漏点；
- 生成可读的诊断说明与学习建议。

Go 与 Graph 负责：

- 状态机和阶段推进；
- 每题最多一次追问；
- 防止空追问、重复问题和死循环；
- 分数、证据折损和掌握度聚合；
- 难度升降策略；
- Tool 权限和可信状态写入；
- 会话取消、退出和完成事件。

### 8. Graph 状态如何传递？

Orchestrator 为一次训练创建独立上下文，保存：

- `AbilityStandard`；
- `StudentProfile` 与 `LearningDiagnosis`；
- `QuestionPlan`；
- `TrainingState`；
- `TrainingAttempt`；
- `EvaluationReport`；
- 长期 `StudentAbilityProfile`。

领域组件不彼此自主协商。Graph 负责调用顺序、条件分支和回调，CLI 与 WebSocket 只消费阶段、问题、评价、报告和计划事件。

---

## 三、知识掌握诊断与自适应策略

### 9. StudentAbilityProfile 当前表示什么？

`StudentAbilityProfile` 的类型名为历史兼容名称，当前主语义是跨训练保存的知识掌握画像。

主要状态包括：

- `knowledge_mastery`：知识点掌握度，范围 0～1；
- `recent_performance`：近期题目、知识点、分数和难度；
- 优势、薄弱点和最近训练时间；
- 原五维能力分作为兼容视图保留。

报告中的知识掌握度使用 0～100，长期画像使用 0～1。系统不会把没有作答证据的知识点直接当作“中等掌握”。

### 10. 一次作答如何形成可追溯证据？

每道题对应一个 `TrainingAttempt`，记录：

- 学科、章节和唯一主知识点；
- 规划难度与实际难度；
- 题目、回答、参考要点和评价量规；
- 正确点、错误点、遗漏点和评分；
- 是否使用提示、追问次数；
- 掌握度证据分和状态变化。

这样可以从最终报告反查到实际问题、学生回答和掌握度变化依据。

### 11. Follow-up 如何工作？

追问不是“答错后直接给答案”，而是针对遗漏点提供一个提示、子问题或苏格拉底式引导。

确定性约束包括：

- 每道题最多追问一次；
- 空追问不会发送；
- 与主问题重复的追问不会发送；
- 用户退出后不会继续生成追问；
- 主回答会先保留，不会被追问结果覆盖。

追问属于带支架表现。最终证据取主回答与折损后追问表现中的较高值，追问分按 0.85 折损，并且同一道题只更新一次掌握度。

### 12. Dynamic Difficulty 如何工作？

难度只有 `easy`、`medium`、`hard` 三档，由 Go 的 Stage Scheduler 调整。

策略会参考：

- 最近三题表现；
- 当前知识点掌握度；
- 连续正确或错误情况；
- 当前 theory、practice、scenario 阶段；
- 当前题目难度。

高表现且掌握度较高时才升级，低表现且掌握度较低时降级；scenario 阶段保持合理难度下限。LLM 不直接决定升降级。

### 13. 学生中途退出如何处理？

一题未答就退出时，不生成没有证据的掌握诊断。已经完成部分题目时，系统仅基于实际完成内容生成阶段性报告。

WebSocket 会取消当前训练 context，旧 generation 不能继续向新会话发送问题；完成事件只发送一次。追问阶段退出时，已提交的主回答仍会保留。

---

## 四、RAG 与训练题库

### 14. RAG 在 Student-Coach 中负责什么？

RAG 的核心职责不是普通知识库问答，而是为当前学生选择下一道更合适的训练题。

检索条件包括：

```text
学科
+ 教材
+ 章节
+ 知识点
+ 当前难度
+ 历史薄弱点
+ 最近训练表现
```

上传资料解析时要求题目具有非空、具体、可检索的 `knowledge_point`。历史缺少元数据的题目仍可兼容召回。

### 15. 当前在线检索链路是什么？

真实在线链路是：

```text
Milvus 向量召回
+
BM25 关键词召回
→ 按文档 ID 去重
→ 拼接候选
→ Cross-Encoder / LLM Rerank
→ 选择训练题
```

仓库中的 `fusion.go` 实现了 RRF，但当前 Orchestrator 没有调用，因此不能宣称线上已使用 RRF。

### 16. 如何保证题库隔离和避免重复？

Milvus 文档带 `user_id` 元数据，BM25Manager 也按用户维护索引。系统优先检索当前学生资料；没有结果时才回退到公共题库，不读取其他学生数据。

题目会按学科、章节和知识点元数据过滤，同一个文档 ID 不会在本轮多个方向中重复使用。

### 17. 资料上传如何处理？

上传文件先计算 SHA-256：

- 同名同内容直接跳过；
- 同名内容变化按更新处理；
- 新文件按新增处理。

文件经解析后生成结构化训练题，并写入 Milvus 与 BM25。更新时先删除该用户、该来源文件的旧题，再写入新版。

---

## 五、Memory、Skill、Tool 与 MCP

### 18. Session State 与长期 Learning Memory 有什么区别？

Session State 由 `TrainingState` 维护，记录当前训练的题目、问答、训练事实、近期分数、当前知识点、难度和掌握状态。

长期 Learning Memory 由 Redis/MySQL 保存，包括：

- 跨训练知识掌握画像；
- 历史训练记录；
- 最近报告和学习轨迹；
- 薄弱知识点和近期表现。

代码中构造了 Eino `shortTermMem`，但当前核心 Graph 使用显式 `TrainingState`，因此不把 `shortTermMem` 宣传为已接入能力。

### 19. 当前有哪些学习 Skill？

Skill Runtime 注册五种可复用教学策略：

| Skill | 教学职责 |
|---|---|
| `concept-tutor` | 概念讲解与理解核验 |
| `quick-quiz` | 快速诊断当前掌握状态 |
| `error-review` | 基于错误证据定位错因并复盘 |
| `guided-practice` | 分步支架与引导式练习 |
| `knowledge-compare` | 易混知识对比与边界检查 |

Skill Runtime 与完整 Graph 是独立入口。当前 Graph 不会自动调用 Skill Registry。

### 20. Tool Calling 的权限边界是什么？

Runtime 保留若干历史 Tool ID，例如：

- `get_student_profile`；
- `get_ability_profile`；
- `get_growth_history`；
- `get_ability_report`；
- `search_training_case`；
- `recommend_training_task`。

名称为兼容标识，描述和返回值已经收敛为学习状态语义。

写 Tool 虽然在 Registry 中注册，但不绑定给 LLM ReAct Runtime。知识掌握度、评价结果和学习轨迹由可信 Go 流程根据 `TrainingAttempt` 写入，模型不能通过对话直接改分。

### 21. MCP 在项目中有什么作用？

MCP 是可选增强，不是核心训练依赖：

- Web Scraper 用于抽取公开学习资料；
- GitHub 搜索仅在编程、数据分析或数字工具学习路径中提供资源参考。

MCP 失败时可以改用文本或文件输入，核心出题、作答评价和掌握度更新不会被阻断。

---

## 六、WebSocket 与 ASR/TTS

### 22. WebSocket 如何实现 Human-in-the-loop？

流程是：

```text
Graph 生成问题
→ WebSocket 发送给学生
→ Graph 阻塞等待 answer channel
→ 学生提交真实回答
→ Graph 恢复执行
→ Evaluator 评价
→ 追问或下一题
```

同一连接只允许一个完整训练运行。重复回答、非等待状态回答和旧会话消息会被拒绝。

### 23. ASR/TTS 如何定位？

语音能力是 AI 学伴交互层，不改变评价逻辑：

```text
Coach TTS 提问
→ 学生语音回答
→ Realtime ASR
→ 失败时 HTTP ASR fallback
→ 文本进入可编辑草稿
→ 学生确认后提交
→ Evaluator
```

语音默认关闭。浏览器不持有供应商 API Key，ASR final 不会自动提交答案或触发评分。

---

## 七、测试、运行与兼容边界

### 24. 当前如何验证项目？

后端：

```bash
cd backend
go test -vet=all ./...
```

前端：

```bash
cd web
npm run build
npm run lint
npm run verify:phase8
```

Evaluation Benchmark 使用 mock LLM 验证确定性编排、Skill 选择、Tool 权限、知识诊断和跨轮状态更新。它不代表真实模型面对所有开放输入时的准确率。

### 25. 如何启动项目？

后端：

```bash
cd backend
go run cmd/main.go web
```

前端：

```bash
cd web
npm install
npm run dev
```

浏览器访问 `http://localhost:5173`，后端健康检查为 `http://localhost:9090/health`。

### 26. 哪些历史名称仍然保留？

为避免破坏存储与已有调用，以下名称仍保留：

- Go module 和 import 路径中的 `interview-agent`；
- MySQL 历史表/列；
- Redis key 和 Milvus collection；
- `InterviewRecord` 等兼容类型；
- `start_interview`、`quit_interview` 和 CLI `interview` 别名；
- 部分 Tool、Service 和 DTO 的历史字段名。

这些名称只存在于兼容边界，不作为产品展示名称。物理项目分区已经统一为：

```text
Student-Coach/
├── backend/
├── web/
├── README.md
└── Student-Coach项目技术问答-Go.md
```

### 27. 当前最重要的技术债是什么？

- 内置旧题库仍有部分题目缺少完整教育元数据；
- RRF 已实现但未进入在线 Graph；
- `shortTermMem` 已构造但未进入核心状态通路；
- Skill Runtime 与 Graph 尚未统一调度；
- 部分兼容能力维度、Dashboard 字段和历史存储命名仍存在；
- 追问证据、掌握度变化和难度边界还需要更多专项测试；
- 真实模型效果仍需要匿名样例、人工标注与持续评测。

处理顺序应优先保证事实链、兼容性和可验证性，不为展示技术关键词增加无业务价值的抽象。

---

## 项目讲解建议

1. 先讲“学生哪里不会、下一步怎么学”，再讲 Eino、RAG、Memory 和 Tool。
2. 明确区分当前在线链路、兼容边界和未接入组件。
3. 强调真实作答、受控追问和确定性状态更新。
4. 不把固定 Graph 节点包装成 Multi-Agent。
5. 不把 mock Benchmark 结果解释为真实教学效果。
6. 不虚构用户规模、准确率、提升率或生产数据。
