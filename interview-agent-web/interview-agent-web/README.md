# 师练 AI Web - 教师教学能力训练前端

基于 React + TypeScript + Vite，通过 WebSocket 提供教师资格、试讲答辩与青年教师发展的完整训练交互。完整产品立意见 [项目级 README](../../README.md)。

## 技术栈

| 类别 | 选型 |
|------|------|
| 框架 | React 19 + TypeScript |
| 构建 | Vite 8 |
| 样式 | Tailwind CSS 4 |
| 状态管理 | Zustand |
| 通信 | WebSocket（训练/ASR）+ REST API（登录、TTS、capabilities） |

## 前置条件

1. **Node.js 20.19+ 或 22.12+**

```bash
node --version   # 确认已安装
```

2. **后端服务已启动**

前端依赖后端提供 API 和 WebSocket 服务（默认 `localhost:9090`）。请先按照 [interview-agent](../../interview-agent-back-end/interview-agent) 后端项目的说明启动后端：

```bash
cd ../../interview-agent-back-end/interview-agent
make infra-up              # 启动 Milvus + Redis + MySQL
go run cmd/main.go web     # 启动后端，监听 :9090
```

## 快速启动

```bash
# 1. 安装依赖
npm install

# 2. 启动开发服务器
npm run dev
```

启动后访问 http://localhost:5173

## 使用流程

1. **注册/登录** — 首次使用需注册账号
2. **上传教师题库**（可选） — 支持教师资格题、试讲答辩题和课堂情境题；不上传时使用内置题库与动态生成
3. **开始训练** — 输入教师岗位/考核标准与教学档案，系统执行完整训练流程：
   - 解析标准 → 诊断训练起点 → 检索教师题库 → 规划训练
   - 教育理论、教学实践、课堂情境三阶段自适应训练
   - 五维形成性评价 + 教学能力提升计划
4. **教研问答** — 可围绕课程标准、教学设计、课堂管理和学习评价进行启发式交流

## 语音训练

语音由后端 capability 和用户开关共同控制。后端保持默认 `SPEECH_ENABLED=false` 时，页面行为与纯文字版本一致；后端开启后，训练设置和输入区会显示“启用语音训练”。

- 问题到达后可自动朗读，并可随时“跳过朗读”。
- 点击“语音作答”后才申请麦克风权限；录音时显示实时 partial。
- 点击“结束识别”后停止麦克风，final 作为可编辑草稿写入原输入框。
- 实时识别降级时继续录音，结束后由后端 HTTP ASR 生成草稿。
- 语音完全失败时保留已有输入和最后 partial，可直接改用键盘。
- 新任务、提交、终止训练、关闭语音、断线和组件卸载都会取消旧播放、麦克风和 ASR WebSocket。

浏览器只访问本项目的 `/api/speech/*` 和 `/ws/speech/asr`，源码和浏览器存储中不应出现 DashScope API Key、供应商 URL 或模型名。ASR final 不会自动触发评分。

首次验收建议在 Chrome/Edge 的正常窗口中依次检查：允许麦克风、拒绝麦克风、开始/停止/重新录制、录音时切换新问题、终止面试后设备占用消失。完整矩阵见[语音面试发布检查表](../../docs/voice-interview-release-checklist.md)。

## 项目结构

```
src/
├── components/
│   ├── LoginPage.tsx         # 登录/注册页
│   ├── ChatWindow.tsx        # 主聊天界面（面试交互）
│   ├── Sidebar.tsx           # 侧边栏（导航 + 连接状态）
│   ├── MessageBubble.tsx     # 消息气泡
│   ├── StageIndicator.tsx    # 面试阶段指示器
│   ├── ScoreCard.tsx         # 答题评分卡片
│   ├── ReportCard.tsx        # 评估报告展示
│   ├── ReviewPlanCard.tsx    # 复习规划展示
│   └── FileUpload.tsx        # 文件拖拽上传
├── hooks/
│   ├── useWebSocket.ts       # 面试控制 WebSocket
│   └── useInterviewSpeech.ts # TTS/麦克风/ASR 生命周期
├── api/
│   ├── ws.ts                 # WebSocket 客户端
│   ├── auth.ts               # 登录注册 API
│   └── speech.ts             # capability、TTS 与 ASR 客户端
├── store/
│   ├── authStore.ts          # 认证状态（Zustand）
│   └── chatStore.ts          # 聊天/面试状态（Zustand）
├── types/
│   └── message.ts            # 消息类型定义
├── App.tsx                   # 根组件
└── main.tsx                  # 入口
```

## 常用命令

```bash
npm run dev       # 启动开发服务器（默认 :5173）
npm run build     # 生产构建（输出到 dist/）
npm run preview   # 预览生产构建
npm run lint      # ESLint 检查
npm run verify:phase8 # Phase 6-8 语音静态/PCM/降级/发布约束检查
```

## 后端连接配置

开发模式下，Vite 代理将请求转发到后端：

- `/api/*` → `http://localhost:9090`（REST API）
- `/ws`、`/ws/speech/asr` → `ws://localhost:9090`（WebSocket）

如果后端端口不是 9090，修改 `vite.config.ts` 中的 proxy 配置。

## 常见问题

**Q: 页面白屏？**

检查后端是否已启动。打开浏览器开发者工具（F12）查看 Console 和 Network，如果看到 `ECONNREFUSED` 或 WebSocket 连接失败，说明后端未运行。

**Q: 登录后无法连接？**

确认后端 `go run cmd/main.go web` 已正常启动且输出了 `[Web] 服务器启动: http://localhost:9090`。
