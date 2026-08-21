# StudentCoach Web

StudentCoach 是面向学生能力提升的 AI Growth Agent。本目录为其 React、TypeScript 和 Vite 交互界面。

前端负责登录、学习目标输入、训练对话、语音交互、过程反馈、能力报告和成长计划展示。完整项目定位见[项目首页](../../README.md)。

## 界面能力

- 用户注册、登录与登录状态恢复；
- 输入学习目标和学生画像；
- 通过 WebSocket 接收流式训练消息；
- 展示训练阶段、任务、追问和即时反馈；
- 展示能力评价报告和成长建议；
- 上传训练资料；
- 在后端允许时使用问题朗读和语音作答；
- 将历史协议映射为新的学生训练消息，页面核心状态不消费历史字段。

## 技术栈

| 类别 | 选型 |
|---|---|
| 界面框架 | React 19 |
| 开发语言 | TypeScript |
| 构建工具 | Vite 8 |
| 样式 | Tailwind CSS 4 |
| 状态管理 | Zustand |
| 内容渲染 | React Markdown |
| 实时通信 | WebSocket |
| 服务接口 | REST API |

## 环境要求

- Node.js 20.19 以上或 22.12 以上；
- npm；
- 已启动的 StudentCoach 后端；
- 推荐使用近期版本的 Chrome 或 Edge，以获得完整语音能力。

## 本地启动

1. 先按照[后端说明](../../interview-agent-back-end/interview-agent/README.md)启动依赖和 Go 服务。
2. 进入 interview-agent-web/interview-agent-web。
3. 运行 npm install 安装依赖。
4. 运行 npm run dev 启动开发服务器。
5. 在浏览器打开 http://localhost:5173。

## 使用流程

1. 注册或登录学生账号。
2. 输入本轮学习目标和必要的学生背景信息。
3. 开始训练，按教练提示完成任务并回答追问。
4. 查看即时反馈，继续补充或修正思路。
5. 训练结束后查看能力评价和成长建议。
6. 下一次训练时，系统会结合历史能力画像推荐更合适的方向。

## 后端连接

开发环境由 Vite 将浏览器请求代理到 http://localhost:9090：

| 浏览器路径 | 后端用途 |
|---|---|
| /api | 登录、注册和语音 REST API |
| /ws | 训练会话 WebSocket |
| /ws/speech/asr | 实时语音识别 WebSocket |

如果后端不使用默认 9090 端口，需要同步调整 vite.config.ts 中的代理目标。

## 语音训练

语音能力由后端开关和用户设置共同控制。后端保持默认关闭时，页面使用纯文字训练。

启用后：

- StudentCoach 的问题可以自动朗读，也可以随时跳过；
- 浏览器只在用户主动开始语音作答后申请麦克风权限；
- 实时识别结果会逐步显示；
- 最终识别文本只进入可编辑输入框，不会自动提交；
- 实时识别失败时，后端可以使用 HTTP ASR 降级；
- 新任务、提交、终止训练、断线或页面卸载会释放旧的音频与麦克风资源。

浏览器只连接本项目后端，不保存 DashScope API Key，也不直接连接语音供应商。

## 可用脚本

| 脚本 | 用途 |
|---|---|
| npm run dev | 启动本地开发服务器 |
| npm run build | 执行 TypeScript 检查并生成生产构建 |
| npm run preview | 本地预览生产构建 |
| npm run lint | 执行 ESLint 检查 |
| npm run verify:phase8 | 执行语音静态、PCM、降级与发布约束检查 |

## 构建与验证

提交前至少运行 npm run build。涉及语音功能时，还应运行 npm run verify:phase8，并在 Chrome 或 Edge 中检查麦克风授权、拒绝授权、停止识别、切换任务、断线和终止训练等场景。

生产构建输出到 dist 目录。

## 主要模块

| 模块 | 职责 |
|---|---|
| components | 登录、聊天、消息、阶段、报告和上传界面 |
| hooks | WebSocket 与语音生命周期 |
| api | 认证、训练连接和语音请求 |
| store | 用户状态与训练状态 |
| types | 前后端消息类型和兼容映射 |

## 常见问题

### 页面无法连接后端

确认后端已启动并能访问 http://localhost:9090/health。随后检查浏览器开发者工具中的 Network 和 WebSocket 连接。

### 登录成功但训练无法开始

确认浏览器请求使用 5173 端口的开发服务，并检查 Vite 代理是否仍指向后端 9090 端口。

### 页面没有语音入口

语音默认关闭。请确认后端已启用 Speech，并检查语音能力接口返回值和浏览器麦克风权限。

### 语音识别没有自动发送

这是预期行为。识别结果只作为草稿写入输入框，学生确认后再主动发送。

## 问题反馈

如需反馈问题或提出改进建议，请使用 [GitHub Issues](https://github.com/haishuiwu/Interview-Agent/issues)。
