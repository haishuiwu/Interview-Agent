# StudentCoach 项目面试题汇总（Go / Eino）

> **项目口径**：StudentCoach 是面向学生长期成长的 AI 能力训练教练。系统围绕学习目标、学生画像、训练任务、教练式追问、能力评价和成长记录形成闭环，持续维护逻辑思维、沟通表达、问题解决、批判性思维和反思能力五维画像。

> **架构口径**：项目使用 Eino Graph 按固定拓扑编排多个领域组件，但这些组件不会自主协商，也不会动态组队，因此不要把它包装成 Multi-Agent 系统。更准确的说法是“Graph 编排的学生能力训练工作流”。

> **可信口径**：LLM 负责理解、生成和提取评价依据；数值评分、能力聚合、画像更新和成长记录保存由 Go 代码控制。离线 Benchmark 验证的是确定性编排逻辑，不代表真实模型在开放输入上的准确率。

> **兼容口径**：核心模型已经使用 AbilityStandard、StudentProfile、LearningDiagnosis、StudentAbilityProfile、TrainingState 和 EvaluationReport。旧请求字段、部分数据库列、文件名和 Graph 节点 ID 只为兼容历史版本保留。

---

## 零、项目定位与业务价值

### 0.1 请用一分钟介绍 StudentCoach 项目

**考察点**

能否把业务问题、核心闭环和技术亮点讲清楚，而不是罗列框架名。

**参考回答**

StudentCoach 是一个面向学生长期成长的 AI 能力训练系统。学生可以提出“我想提升表达能力”或“帮我训练一下”，系统会结合学习目标、当前能力画像和历史训练记录，选择训练方式、检索合适案例、组织任务并通过追问暴露学生的思考过程。

它和普通问答助手最大的区别是训练结束后不会只返回一段建议，而是由 AbilityEvaluator 生成评价依据，再由 Go Service 聚合分数、更新五维 StudentAbilityProfile 并保存 GrowthRecord。下一次训练会读取这个画像，优先训练当前弱项，同时调整任务和难度。

工程上保留了 Eino Graph、RAG、Memory、Skill、统一 Tool Registry、WebSocket、Speech 和 MCP，并增加了使用 mock LLM 的离线 Evaluation Benchmark。

---



---

### 0.3 StudentCoach 与通用聊天机器人有什么区别？

**考察点**

能否说明 Agent 产品的专用能力和状态设计。

**参考回答**

通用聊天机器人主要根据当前问题和有限对话历史生成回答，通常没有稳定的训练目标、领域状态和可验证的成长结果。StudentCoach 则有明确的训练流程和长期状态。

系统先解析学习目标，再分析学生已有证据，随后根据能力差距规划任务。StudentCoach 每次只提出一个问题，通过澄清、证据、假设、反例和迁移追问定位能力问题。训练结束后形成可追溯报告，并把训练前后分数、优势、短板和时间写入能力画像。

因此它不是“更会聊天”，而是把 LLM 放进一个由 Go 业务规则约束的长期训练流程。

---

### 0.4 这个项目最值得讲的技术亮点是什么？边界又是什么？

**考察点**

能否区分核心贡献、工程能力和未经验证的效果。

**参考回答**

最值得讲的是学生能力画像闭环。系统既有单轮 TrainingState，也有跨轮 StudentAbilityProfile；LLM 提取作答中的命中点和遗漏点，Go 根据证据覆盖计算单题分和各能力分，再把当前证据与历史分聚合，保存 GrowthRecord。这样第二次训练可以基于真实历史选择弱项，而不是随机出题。

第二个亮点是统一 Tool Registry。StudentCoach 在带有认证运行时的上下文中，可以自主调用画像、历史、报告、案例检索和任务推荐工具，但 Tool 不包含评价决策，Skill 也不直接访问数据库。

边界是：当前 29 条 Benchmark 使用确定性 mock LLM，证明的是调用链和聚合逻辑正确，不证明真实模型对所有学生输入都达到 100% 准确率；系统也只做形成性训练反馈，不替代教师或学校作高风险判断。

---

## 一、Eino Graph 与领域架构

### 1. 为什么使用 Eino Graph，而不是让一个 ReAct Agent 自主完成全部流程？

**考察点**

是否理解确定性工作流与自主 Agent 的取舍。

**参考回答**

完整训练存在严格依赖：先形成能力标准，再分析学生画像，然后规划任务、训练、评价和更新画像。如果把所有步骤交给一个 ReAct Agent 自主决定，它可能跳过画像读取、过早生成任务，或者在没有真实评分时调用写入工具。

Eino Graph 适合表达这种稳定流程。每个节点只承担一个领域职责，拓扑决定执行顺序和终止分支，Go 结构体保存中间结果，失败可以定位到具体阶段。

项目中虽然有多个以 Agent 命名的组件，但它们不自主通信，也没有协商或动态分工，所以我不会称它为 Multi-Agent。它是中心化 Graph 编排，Tool Calling 只发生在需要数据能力的 StudentCoach 节点。

---

### 2. 当前 Graph 的执行拓扑是什么？

**考察点**

能否准确描述真实代码，而不是根据 README 猜测。

**参考回答**

逻辑流程是：

1. AbilityAnalyzer 把学习目标、课程标准或能力要求转为 AbilityStandard；
2. StudentProfileAnalyzer 根据学生输入形成 StudentProfile 和 LearningDiagnosis；
3. QuestionPlanner 读取长期画像与薄弱点，先规划方向，再检索或生成任务；
4. StudentCoach 逐题训练、评分、追问并更新会话内画像；
5. weak review 节点对低于 60 分的题目提供巩固材料；
6. AbilityEvaluator 生成报告依据，Go 计算最终分数；
7. StudentGrowthService 更新长期画像和 GrowthRecord；
8. GrowthPlanner 生成下一步成长计划。

Graph 在 StudentCoach 节点后有一个条件分支：如果学生一题未答就退出，直接结束；只要已有有效作答，就继续生成基于现有证据的报告和计划。

代码中的节点 ID 已收口为 ability_analysis、student_profile_analysis、training、growth_plan，节点实现与领域组件使用同一套学生成长语言。

---

### 3. Graph 节点之间如何传递状态？为什么不让组件互相调用？

**考察点**

对编排状态、耦合和可测试性的理解。

**参考回答**

组件之间没有直接通信。Orchestrator 创建单次 trainingCtx，由 Graph 节点闭包读取和写入，里面保存 Session、AbilityStandard、StudentProfile、LearningDiagnosis、QuestionPlan、TrainingState、EvaluationReport 和长期能力画像。

这种设计的优点是依赖方向单一：领域组件只接收自己的输入，Graph 负责顺序、分支和回调。CLI 与 WebSocket 通过 TrainingCallbacks 接收阶段、问题、评分、报告和成长计划，不需要让 Agent 感知具体交互层。

当前 Graph 的输入输出类型仍是简单字符串，真正状态由闭包持有，这是为了保持既有拓扑的折中。后续如果重构，可以把 Graph State 显式类型化并增加节点重试检查点，但不能在没有收益评估时为了“更像框架”而改动稳定主链路。

---

### 4. 领域模型迁移时，怎样兼容旧 API 又不污染核心模型？

**考察点**

兼容设计、边界隔离和演进策略。

**参考回答**

核心原则是“新语义进核心，旧语义止于边界”。WebSocket 的 ClientMsg 使用 learning_goal 和 student_profile，历史 assessment、profile、jd、resume 被放进 LegacyTrainingInputDTO，由 adaptTrainingInput 在进入 Graph 前转换。

前端同样先把历史消息映射为 start_training 的新字段，核心状态不继续消费招聘字段。数据库没有改表，因此部分历史列名仍存在存储适配层，但加载后会转换成 StudentAbilityProfile 或 GrowthRecord 使用。

Graph 节点 ID 和 Agent 文件名已经完成领域命名收口；旧协议与存储字段仅停留在兼容边界，不进入核心业务语义。

---

### 5. 为什么选择 Go 和 Eino，而不是 Python LangChain 或 Java Spring AI？

**考察点**

框架选型是否与工程目标一致。

**参考回答**

项目后端使用 Go，需要同时维护 WebSocket 长连接、并发会话、RAG 组件和数据存储。Go 的 goroutine、context 取消、接口和静态类型适合这种服务形态。

Eino 是原生 Go 的 LLM 应用框架，提供 ChatModel、Graph、Retriever、Tool 和 ReAct Agent 等抽象，还有 Milvus 扩展，能够直接接入现有 Go 服务。LangChain 的主生态在 Python，Spring AI 属于 Java 生态，都会引入语言或团队栈切换成本。

选 Eino 不是因为框架越多越好，而是它覆盖项目需要的编排和工具抽象，同时允许 BM25、Memory 和 StudentGrowthService 继续保持普通 Go 组件，业务规则不必塞进框架。

---

### 6. 完整训练 Graph 与五类 Skill 是什么关系？

**考察点**

能否解释两个训练入口，避免把 Skill 和 Graph 混为一谈。

**参考回答**

WebSocket 有两个入口。start_training 启动完整 Graph，适合从目标分析、画像诊断一路执行到能力报告和成长计划；普通 chat 消息会先匹配 Skill，适合快速进行某一能力的多轮专项训练。

Skill 是可插拔的训练策略，定义适用场景、训练目标、教练规则和评价维度。它维护自己的 SkillState，可以检索 RAG 参考材料，但不直接读写数据库。完整 Graph 中的 StudentCoach 则可以通过 Tool Registry 读取长期画像、历史和案例，并在评价阶段由 StudentGrowthService 保存成长结果。

因此 Skill 不等于一个新 Agent，也不等于一条新 Graph。它是介于无状态 Tool 和完整工作流之间的有状态训练能力。

---

## 二、学生能力画像与可信评价

### 7. StudentAbilityProfile 为什么是项目的核心模型？包含哪些数据？

**考察点**

是否理解“长期成长”需要什么状态，而不只是会背字段。

**参考回答**

TrainingState 只描述当前会话，例如当前题号、难度、连续表现和问答历史；StudentAbilityProfile 则跨训练存在，是系统实现长期个性化的依据。

它包含 student_id、summary、ability_scores、strengths、weaknesses、growth_history 和 last_training_time。能力分范围为 0 到 1，固定对应逻辑思维、沟通表达、问题解决、批判性思维和反思能力五个维度。

GrowthHistory 不只保存训练后的分数，还记录 session、学习目标、训练前分数、训练后分数、变化量、综合得分和训练时间，因此能够解释“这次为什么变化”，而不是只保留一个不可追溯的最终值。

---

### 8. 学生第一次训练没有历史画像时，系统怎么处理？

**考察点**

冷启动策略和避免虚构数据的意识。

**参考回答**

GetAbilityProfile 在没有历史记录时返回 student_id 和空的 ability_scores，不会为了看起来完整而初始化一组 0.5。因为没有训练证据时，0.5 也只是人为假设。

题目规划仍可使用本轮 AbilityStandard、StudentProfile 和 LearningDiagnosis；起始难度默认 medium。StudentCoach 完成作答后，Go 根据真实证据形成对应能力分。某个能力第一次出现时直接采用本轮证据分，训练结束后保存完整画像和第一条 GrowthRecord。

这种设计把“未知”和“中等水平”区分开了，避免冷启动默认值长期影响画像。

---

### 9. 为什么说 LLM 不直接决定最终分数？Go 是怎么评分的？

**考察点**

可信评价的实际实现，而不是一句“规则兜底”。

**参考回答**

单题评价时，LLM 只输出 feedback、key_points_hit 和 key_points_missed。Go 先识别空回答、“不会”“不知道”“跳过”等情况并给 0 分；其他回答按命中点数量除以命中点与遗漏点总数计算 0 到 100 的证据覆盖分。

追问也由规则决定：得分在 30 分以上、80 分以下且存在遗漏点时才触发，避免对完全不会或已经较完整的回答机械追问。

最终报告的各能力分由该能力关联题目的实际得分取平均，综合分由已完成题目平均得到，A、B、C、D 分界分别为 85、70、50。LLM 仍负责提取评价证据，因此并非完全消除了模型误差，但它不能直接写 overall_score 或 ability_scores，数值权力被收回到可测试的 Go 代码。

---

### 10. 训练结束后，长期能力分如何更新？

**考察点**

能否解释聚合公式、数据范围和保存顺序。

**参考回答**

AbilityEvaluator 先生成报告依据，Go 再把报告中的本轮能力分标准化到 0 到 1。某个能力第一次出现时，长期画像直接采用本轮证据分；已有历史分时，当前实现使用简单指数平滑：新分等于历史分与本轮证据分的平均值，两者各占 50%。

StudentGrowthService 同时保存 before_scores、after_scores 和 score_changes，合并本轮优势与短板，更新时间并追加 GrowthHistory。优势和短板分别最多保留 12 条，成长历史最多保留最近 50 条，避免画像无限增长。

画像保存成功后再保存 GrowthRecord。当前 50% 权重是可解释基线，不应宣传成教育学最优参数；后续应根据真实纵向数据比较不同时间衰减和置信度策略。

---

### 11. Memory 如何影响学生第二次训练？

**考察点**

能否说清楚画像不是“只存不读”。

**参考回答**

QuestionPlanner 之前会读取长期 StudentAbilityProfile，把已有五维能力分写入规划上下文；同时读取历史 WeakPoints，并按当前 AbilityStandard 过滤无关弱项，避免这次练表达却被旧的其他主题带偏。

StudentCoach 的 Tool 使用策略要求：目标明确时读取对应能力历史，目标不明确时先获取长期画像并选择最低分维度，再检索案例和推荐任务。例如 communication 为 0.55、logical_thinking 为 0.80，学生说“帮我训练一下”时，Go 的 RecommendTrainingTask 会优先映射到 communication-training。

画像还影响初始难度。这样 Memory 同时参与方向、任务和难度，而不是只在报告页面展示历史数据。

---

### 12. 动态难度是怎么设计的？为什么不是每题都升降？

**考察点**

规则设计、稳定性和画像参与方式。

**参考回答**

系统先根据长期画像中最低能力分确定本轮起点：无历史时使用 medium；最低分低于 0.45 时从 easy 开始；达到 0.80 及以上时从 hard 开始；其余为 medium。

训练按 theory、practice、scenario 三个阶段推进。每进入一个新阶段，难度重置为本轮画像确定的起点，连续计数清零，避免上一阶段的表现错误影响不同类型任务。

阶段内单题得分达到 70 记为答好，否则记为答得不好；连续两题答好提升一级，连续两题未达到标准降低一级。要求连续两题是为了减少一次偶然失误带来的抖动，难度始终限制在 easy、medium、hard 三档。

---

### 13. 学生中途退出时，如何保证评价不失真？

**考察点**

异常中断、证据边界和 WebSocket 生命周期。

**参考回答**

学生可以随时终止训练。Orchestrator 根据是否已经形成有效作答分两种情况处理：一题未答就退出时，Graph 直接结束，不生成没有证据的诊断；已经完成部分题目时，仍基于 QAHistory 中的真实作答生成报告和成长计划。

如果在追问阶段退出，主回答会先写入 QAHistory，追问不会伪造结果。报告中的完成率、平均分和能力分只使用实际完成的数据，不能把未作答当成能力不足。

WebSocket 使用 context 取消训练 goroutine，并通过 generation、completionSent 和 sessionClosed 防止旧会话继续向新会话发送问题或重复完成消息。

---

## 三、Skill 与教育 Tool Calling

### 14. 为什么设计五个训练 Skill？它们如何避免只是换 Prompt 名称？

**考察点**

Skill 是否有真实边界和教学设计。

**参考回答**

五个 Skill 与长期能力画像维度一一对应：logical-thinking、communication-training、problem-solving、critical-thinking 和 reflection-training。

它们不只是名称不同。每个 Skill 都有结构化 TrainingSkillDefinition，包含适用场景、训练目标、Agent 行为规则和评价维度。例如逻辑训练要求学生给出每一步依据，并用反例检查推理；表达训练强调听众、目的、结构和回应；问题解决要求明确目标与约束、比较方案并设置验证标准。

五个 Skill 复用 AbilityTrainingSkill 的多轮状态机和 RAG 能力，差异集中在可检查的定义与触发词上。这样既避免复制五套流程，又能独立测试每类能力的匹配和评价维度。

---

### 15. Skill Registry 如何选择 Skill？发生冲突时怎么办？

**考察点**

意图选择的确定性、优先级和边界。

**参考回答**

当前 Skill 使用关键词包含匹配。Registry 按注册顺序遍历，返回第一个命中的 Skill，因此先注册者优先级更高。五个 Skill 使用各自的自然语言触发词，例如“表达能力”“方案设计”“判断可信”“学习复盘”。

Skill 会话激活后，普通输入默认交给当前 Skill；如果用户明确提出另一种能力训练意图，WebSocket 会清空原状态并切换到新 Skill，避免把“帮我练批判性思维”误当成上一题答案。

这种规则匹配延迟低、结果可测试，当前有 15 条 Skill Selection Benchmark。局限是复杂表达和触发词重叠时泛化有限，后续可以增加置信度或分类器，但需要保留确定性回退和可解释优先级。

---

### 16. Skill 的多轮状态如何管理？

**考察点**

有状态训练与普通函数调用的区别。

**参考回答**

SkillState 保存 SkillName、UserID、Round、StartedAt 和自定义 Data。Data 中记录训练目标、上一任务、已发现的能力问题和维度观察分，下一轮 Handle 会基于这些数据继续训练。

首次进入时，Skill 可以按用户从 RAG 获取最多三条参考材料，然后只生成一个训练任务。后续每轮先反馈有效表现，再定位一个能力问题、给出可执行建议并决定下一次追问；Round 达到 4 时结束并输出总结。

状态超过 30 分钟自动过期，也支持 /quit、/exit、“退出”和“结束”。这个 SkillState 保存在当前 WebSocket 会话中，不直接写数据库；完整的长期画像更新仍由 Graph 和 StudentGrowthService 负责。

---

### 17. Tool、Skill 和 Graph 中的领域组件有什么区别？

**考察点**

能否解释三种抽象的职责和生命周期。

**参考回答**

Tool 是无状态或短调用的数据能力，例如查询画像、检索案例或保存记录。它应该有明确输入输出，不承担完整训练策略。

Skill 是用户意图触发的有状态多轮训练能力。它决定如何训练某一种能力，可以维护 SkillState 和使用 RAG，但不能直接访问数据库。

AbilityAnalyzer、StudentCoach、AbilityEvaluator 等领域组件是完整 Graph 中的阶段执行者，由 Orchestrator 按固定顺序调用。它们不会像自主 Agent 一样互相协商。三者的关系可以概括为：Tool 提供数据动作，Skill 提供专项训练策略，Graph 负责完整业务流程。

---

### 18. 统一 Tool Registry 注册了哪些工具？为什么要统一注册？

**考察点**

Tool 边界、注册治理和测试能力。

**参考回答**

Registry 当前注册八个教育数据工具：

1. get_student_profile：读取学生基础画像和已知短板；
2. get_ability_profile：读取长期五维能力画像；
3. get_growth_history：按能力查询历史训练；
4. get_ability_report：读取最近能力报告；
5. search_training_case：按短板检索训练案例；
6. recommend_training_task：由 Go 服务结合目标和画像推荐任务与 Skill；
7. update_ability_profile：根据真实评价更新画像并保存成长记录；
8. save_growth_record：兼容只保存历史记录的场景。

统一 Registry 可以拒绝重名、保持稳定顺序、集中暴露给 Eino ReAct Agent，并允许测试直接替换 StudentGrowthService。Tool 描述同时约束调用时机，避免模型为了展示能力而无意义调用。

---

### 19. “我想提升自己的表达能力”会触发怎样的 Tool 调用链？

**考察点**

是否能把意图、历史、RAG、推荐和 Skill 串起来。

**参考回答**

在带认证运行时的完整训练中，StudentCoach 首先调用 get_ability_profile 获取长期画像；目标已经明确为表达能力，因此调用 get_growth_history 查询 communication 的历史训练。

接着调用 search_training_case 检索与表达短板相关的案例，再把学习目标、communication 和选中的案例交给 recommend_training_task。最终任务必须采用 Go Service 返回的 skill_name 和 training_task，对应 communication-training，而不是让模型自行改成其他 Skill。

如果学生只说“帮我训练一下”，调用链会先读取画像，再由 RecommendTrainingTask 选择已有能力分中的最低维度。没有任何历史时，服务使用 problem-solving 作为可解释的默认训练焦点。

---

### 20. 怎样防止 LLM 越权调用写工具或访问其他学生数据？

**考察点**

Tool Calling 的身份、安全和业务授权。

**参考回答**

student_id 不由模型参数提供，而是 WebSocket 完成认证后通过 educationtool.WithRuntime 注入 context。每个 Tool 调用 resolveRuntime 获取当前学生和 StudentGrowthService，因此模型不能修改参数来切换用户。

只有 context 同时包含学生身份和服务时，StudentCoach 才构建带工具的 ReAct Agent；否则退化为普通模型生成。ReAct 最大步数限制为 20，避免无限工具循环。

写入方面，完整 Graph 不让模型决定持久化时机，而是在 AbilityEvaluator 完成后直接调用 StudentGrowthService。不过 Registry 仍向 ReAct Agent 暴露 update_ability_profile，当前主要依赖工具描述阻止它在没有真实评价时调用；Service 能做范围归一化和聚合，却不能证明输入一定来自可信评价。

所以跨学生访问已经由身份上下文限制，但“写入数据来源可信”仍有改进空间。更严格的方案是训练提问阶段只注册读工具，评价结束后由 Go 直接写入；或者给写工具增加服务端生成的 session/evaluation 凭证，而不是只依赖 Prompt。

---

## 四、RAG、训练数据与检索评估

### 21. 为什么同时使用 Milvus 向量检索和 BM25？

**考察点**

是否理解语义召回和关键词召回的互补关系。

**参考回答**

学生训练输入经常是自然语言，例如“我表达时总是没有重点”。向量检索可以召回“结构化表达”“信息组织”等语义接近但字面不同的案例；BM25 对“因果推理”“证据来源”“方案验证”等明确词语和能力标签更敏感。

只用向量检索可能漏掉关键术语和精确能力标签，只用 BM25 又难以处理同义表达。因此主流程按同一查询分别调用 Milvus 和 BM25，再按文档 ID 去重并交给重排器。

组合检索的目标不是让链路更复杂，而是在训练题库规模不大、学生表达差异较大的情况下提高候选覆盖率。是否真正提升效果要用标注数据比较，而不能只凭架构图判断。

---

### 22. 当前主链路怎样融合和重排？RRF 是否已经在线使用？

**考察点**

能否区分“仓库有实现”和“主流程实际调用”。

**参考回答**

当前 Orchestrator 的主链路不是把向量分数和 BM25 分数直接相加，而是依次收集两路结果，按文档 ID 去重，再使用可配置 RerankStrategy 重排，最终为一个基础理解方向取 Top 1。

默认重排器是 DashScope gte-rerank-v2 cross-encoder，也可以切换到通用 LLM 或 none。cross-encoder 的网络错误、HTTP 错误或无效索引会降级到原始候选顺序并截断，不阻断训练。

仓库确实有 k=60 的 RRF MultiRetriever，但当前 Orchestrator 没有调用它，所以不能声称线上流程是“Milvus + BM25 + RRF”。正确表述是：RRF 是可复用和可对照的实现，当前主链路采用 ID 去重加 Rerank。

---

### 23. RAG 如何实现学生数据隔离和公共题库冷启动？

**考察点**

多租户检索、隐私边界与回退策略。

**参考回答**

Milvus 文档带 user_id 元数据，RetrieveByUser 在向量检索时加入用户过滤；BM25Manager 也按用户维护独立索引。StudentCoach、Skill 和训练案例 Tool 都从认证上下文获得当前 userID。

主流程先检索当前学生的私有题库。只有两路都没有结果时，才回退到 default_user 的系统训练题库，不会查询其他学生的数据。这样既保证隔离，也解决新用户没有私有资料时的冷启动。

当前 BM25 是按用户保存在单机内存中的实现，规模扩大后会随用户和题库数量增长。可以迁移到 OpenSearch 或 Elasticsearch 并保留 tenant/user 过滤，但迁移前应先确认它确实是瓶颈。

---

### 24. 为什么训练任务规划分为“方向规划”和“题目组装”两阶段？

**考察点**

RAG 与生成式出题的边界设计。

**参考回答**

第一阶段 QuestionPlanner 只决定训练方向，包括主题、题型、难度、检索词、目标能力和学生上下文。这样可以先检查覆盖是否合理，而不是让 LLM 一次生成全部题目后再猜它为什么这样分配。

第二阶段按方向组装任务。theory 类型优先使用用户题库和系统题库中的原题，命中后代码直接保留题干、参考要点、难度、能力标签和来源；practice 与 scenario 更依赖学生画像和具体情境，因此交给 LLM 动态生成。基础题没有命中时也由 LLM 兜底。

这种两阶段设计把“练什么”和“题目怎么来”分开，既能利用稳定题库，又保留个性化情境生成能力，还可以统计真实题库命中率。

---

### 25. 训练资料上传如何去重、更新和写入 Milvus？

**考察点**

文件处理、幂等性、数据替换和批量写入。

**参考回答**

WebSocket 收到 base64 文件后，先对原始字节计算 SHA-256。Redis 按 userID 和 filename 保存内容哈希：同名同内容直接跳过；同名但内容变化标记为更新；不同文件名按新增处理。

文件被解析为文本后，由 QuestionParser 和 LLM 转成结构化训练题，并校验题型、题干、参考要点、难度和能力标签。更新时先按 user_id 与 source_file 删除 Milvus 中的旧题，再写入新版；BM25 用户索引也同步更新。

Milvus 使用 text-embedding-v3 的 1024 维向量和 COSINE 距离，写入时每批 10 条，降低单次 Embedding 和插入请求过大导致的失败范围。批量大小是工程基线，不是经过吞吐压测得出的最优值。

---

### 26. RAG 的离线评估指标是什么？它和 StudentCoach Benchmark 有什么区别？

**考察点**

能否区分检索质量与 Agent 业务效果。

**参考回答**

RAG 离线评估使用人工标注的 query 与 relevant_doc_ids，完整执行 Milvus、BM25、去重和 Rerank。主要指标是 Recall@10、Recall@20 和 MRR：Recall@K 衡量前 K 条覆盖了多少相关文档，MRR 关注第一个相关文档出现得有多靠前。

报告还记录 Embedding、向量维度、TopK、BM25 参数和重排策略，支持按主题、难度分组以及定位最差样本。另有 LLM 评估器可以观察 faithfulness、relevance 和 completeness，但这类模型评分不能替代人工标注检索指标。

StudentCoach Evaluation Benchmark 评估的是 Skill 选择、Tool 调用、能力诊断和成长闭环，两套评估回答不同问题。RAG 指标高不等于学生成长有效，Agent 调用链正确也不等于检索结果相关。

---

## 五、Memory、WebSocket、Speech 与 MCP

### 27. 代码里既有 20 条 ShortTermMemory，又只把最近 3 轮交给 StudentCoach，主链路到底用哪个？

**考察点**

是否真正读过调用关系，并能诚实指出未接入组件。

**参考回答**

当前完整训练主链路使用 TrainingState.QAHistory。StudentCoach 每次提问只取最近 3 轮原始问答，再附加动态 StudentAbilityProfile，控制上下文长度并保留最新细节。

Orchestrator 确实创建了最大 20 条消息的 ShortTermMemory，但当前没有调用它的 Add 或 Get，因此不能声称主训练已经使用这 20 条滑动窗口。它是保留下来的记忆模块和后续统一会话记忆的基础。

面试时应该明确区分“存在的组件”和“实际执行路径”。如果后续接入，应统一 QAHistory 与 ShortTermMemory 的职责，避免两份短期上下文出现不一致。

---

### 28. 历史 WeakPoints 和 StudentAbilityProfile 有什么区别？为什么现在两套都存在？

**考察点**

领域迁移中的兼容状态和重复模型治理。

**参考回答**

WeakPoints 是旧长期画像中的主题级统计，记录最近分数、考察次数、错误次数和最后出现时间。低于 60 分的新主题会加入，达到 80 分会移除；读取时过滤超过 30 天的数据，按分数升序取最弱的 10 条。

StudentAbilityProfile 是新核心模型，面向五个稳定能力维度，记录 0 到 1 的能力分、优势、短板和 GrowthHistory。它承担长期成长主线，WeakPoints 主要用于兼容历史题目标签和补充规划上下文。

两套同时存在是渐进迁移的结果，不是理想终态。当前 QuestionPlanner 会把二者合并使用，并按本轮 AbilityStandard 过滤 WeakPoints。后续可以设计一次数据迁移，把有效主题证据归并到能力画像，减少双写和语义重复。

---

### 29. 为什么 Memory 使用 Redis 和 MySQL 组合存储？

**考察点**

缓存、持久化、一致性和降级策略。

**参考回答**

CombinedStore 读取时先查 Redis，未命中再读 MySQL并回填；写入时先写 MySQL，再写 Redis。MySQL 是持久化事实来源，Redis 用于降低频繁读取画像和会话的延迟。

Redis 写入失败只记录日志，不影响已经成功的 MySQL 主写；MySQL 失败则返回错误，避免只在缓存中形成不可恢复的数据。Session 从 MySQL 回填 Redis 时使用两小时 TTL，能力画像和文件哈希按各自存储策略管理。

这种 cache-aside 加主库优先的方案适合当前规模，但还不是强事务双写。更高一致性要求下可以增加 outbox、版本号或异步修复任务，避免 Redis 中短暂读取旧画像。

---

### 30. WebSocket 如何处理并发训练、重复回答和旧消息串线？

**考察点**

Go 并发控制、状态机和长连接生命周期。

**参考回答**

每个 WSSession 只允许一个完整训练运行。beginTraining 在互斥锁内检查 trainingRunning，创建带取消函数的 context 和容量为 1 的 answerCh，并递增 trainingGeneration。

服务发送问题前先设置 awaitingAnswer，避免客户端立即回答时落入状态间隙。handleAnswer 只有在正在训练、等待回答、尚未完成且连接未关闭时才把答案写入 channel；成功后立即清除等待状态，重复提交会返回 ANSWER_NOT_EXPECTED。

所有训练消息都携带当前 generation 的逻辑校验。退出、断线或新生命周期会取消 context，旧 goroutine 即使晚返回也不能向当前连接继续发送。completionSent 保证完成消息只发送一次。

---

### 31. Speech 和 MCP 在 StudentCoach 中各自解决什么问题？如何降级？

**考察点**

外围能力是否与核心业务解耦，以及安全边界。

**参考回答**

Speech 是交互增强，不改变出题、评分和画像更新。TTS 朗读 StudentCoach 问题，ASR 把语音转为可编辑草稿；实时 ASR 失败时可以使用同一受限音频缓冲走 HTTP ASR。最终转写不会自动提交，学生仍需确认。

语音默认关闭，浏览器不持有 DashScope Key。服务端限制字符数、音频大小、回答时长、并发和超时，并使用精确 Origin 白名单。questionID 和会话状态用于避免旧音频结果写入新问题。

MCP 保留两个可选能力：Playwright 抓取网页中的学习目标或能力标准，GitHub 搜索只在成长计划需要真实数字化学习资源时使用。MCP 初始化或调用失败时，用户可以改用文件/文本输入，成长资源由 LLM 兜底，核心训练不应被外围工具阻断。

---

## 六、StudentCoach Evaluation Benchmark

### 32. 为什么已经有 go test，还要单独建立 Evaluation Benchmark？

**考察点**

是否理解普通单元测试与 Agent 效果评测的区别。

**参考回答**

普通单元测试适合验证函数边界，例如 Registry 是否拒绝重复工具、评分公式是否正确、存储读写是否符合预期。但 StudentCoach 的关键价值包含跨组件行为：输入能否选对 Skill、模型是否按预期调用 Tool、评价证据能否映射为能力分、更新后的画像能否影响下一次训练。

evaluation 目录把这些行为定义为标注样例和业务指标，仍然复用真实 StudentCoach、Skill Registry、Tool Registry 和 StudentGrowthService，而不是另写一套评测实现。

这样代码重构后不仅能看到“函数没报错”，还可以看到 Skill Accuracy、Tool Selection Accuracy、Diagnosis Accuracy 和 Growth Loop Success Rate 是否下降。

---

### 33. 当前 Benchmark 如何做到离线、确定性和可读？

**考察点**

mock LLM、测试数据和指标报告设计。

**参考回答**

测试数据全部放在 evaluation/testdata，共 29 条标注样例：Skill 选择 15 条、Tool 调用 7 条、能力诊断 6 条、成长闭环 1 条。

Skill 测试直接运行真实 Registry；Tool 测试使用 scriptedToolModel 按样例产生 Eino ToolCall，并用 recordingGrowthService 记录真实调用顺序；能力诊断 mock 只返回预设命中点和遗漏点，分数仍由 StudentCoach 和 AbilityEvaluator 的 Go 逻辑计算。

每项测试输出 Markdown 表格形式的通过数、样例数和准确率，go test -v ./evaluation 可以直接查看报告。mock 不访问真实模型、数据库、MCP 或网络，因此结果可重复，也不会因为 API 波动导致 CI 不稳定。

---

### 34. Growth Loop Benchmark 具体证明了什么？没有证明什么？

**考察点**

闭环测试、因果边界和指标解读。

**参考回答**

测试先创建 communication 为 0.55、logical_thinking 为 0.80 的画像，再提交本轮 communication 75 分的真实评价输入。StudentGrowthService 按 50% 历史分和 50% 本轮分更新，communication 得到 0.65，同时断言 GrowthRecord 已保存。

第二次训练使用“帮我训练一下”，StudentCoach 必须依次调用 get_ability_profile 和 recommend_training_task，并从工具结果选择 communication-training。这个测试覆盖了“读取旧画像—更新—保存—再次读取—弱项优先”的完整闭环。

它证明的是当前代码在该确定性场景下行为正确，没有证明 0.65 就是学生真实能力，也没有证明真实 LLM 对所有自然语言都能选对工具。后续需要扩大真实表达样例、做模型回放和人工评价，才能讨论开放场景效果。

---

## 七、工程扩展与复盘

### 35. 如果要支持 100 名学生同时训练，最先优化什么？

**考察点**

能否识别真实瓶颈，而不是只说“加机器”。

**参考回答**

第一瓶颈是模型调用。一次完整训练包含能力分析、画像诊断、方向规划、出题、逐题提问、评价依据、追问、报告和计划，还可能有 Tool Calling。应先做模型级限流、超时、重试、熔断和成本观测，对可复用分析做缓存，并控制单会话并行度。

第二是当前每次 AskQuestion 都会构建 ReAct Agent，每次 RunTraining 都编译 Graph，可以在保证 context 隔离的前提下复用静态工具配置和预编译拓扑。BM25 按用户保存在单机内存，规模增长后需要外置检索服务。

第三是状态一致性与水平扩展。WebSocket 连接可以由 Go 承载，但多实例下要使用粘性路由或外部会话协调；画像更新目前是“读取—计算—写回”，同一学生并发训练可能发生丢失更新，需要版本号、事务或乐观锁。扩容前应先用 tracing 分解 LLM、RAG、存储和连接等待时间，再针对主要瓶颈处理。

---

### 36. 这个项目目前有哪些技术债？下一步你会怎么改？

**考察点**

项目复盘是否具体、诚实并能排优先级。

**参考回答**

第一类技术债来自渐进迁移：Graph 节点 ID、部分文件名和存储列仍是历史名称，WeakPoints 与 StudentAbilityProfile 也有语义重叠。下一步应先做可回滚的数据迁移和 adapter 测试，再逐步清理内部兼容，而不是直接改数据库。

第二类是链路一致性：ShortTermMemory 已实现但主训练没有实际使用；快速 Skill 模式的维度观察分没有进入长期画像；RRF 组件存在但主链路使用的是去重加 Rerank。应明确唯一事实来源，删除或接入长期未使用的能力，并让文档、评测与线上路径保持一致。

第三类是效果验证：当前 Tool Benchmark 使用脚本化调用，Skill 匹配仍是关键词，能力证据也依赖 LLM 提取。下一步优先收集匿名真实训练样例，增加真实模型回放集、工具误调用率、人工诊断一致性和跨轮提升指标，再决定是否引入分类器、更复杂的画像权重或检索策略。

---

## 面试回答使用建议

1. 先讲业务闭环，再讲 Eino、RAG 和 Tool 等实现手段。
2. 把“当前主链路”“仓库预留能力”“后续优化”分开，不把未接入组件说成线上能力。
3. 遇到评分追问时主动说明：数值由 Go 控制，但评价证据仍来自 LLM，因此仍需要标注集和人工校验。
4. 遇到 Multi-Agent 追问时明确：当前是固定 Eino Graph，不是自主协作的 Multi-Agent。
5. 遇到 100% Benchmark 追问时说明：这是 29 条 mock 基线，不等于真实模型准确率。
6. 项目没有真实用户效果数据时，不虚构提升率、并发量或生产规模。
