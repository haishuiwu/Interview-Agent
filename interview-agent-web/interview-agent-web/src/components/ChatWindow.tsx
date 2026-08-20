/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { MessageBubble } from './MessageBubble'
import { FileUpload } from './FileUpload'
import { StageIndicator } from './StageIndicator'
import { useChatStore } from '../store/chatStore'
import { useAuthStore } from '../store/authStore'
import { useInterviewSpeech } from '../hooks/useInterviewSpeech'
import type { WSClient } from '../api/ws'
import type { ClientMessage } from '../types/message'

interface ChatWindowProps {
  wsRef: React.RefObject<WSClient | null>
}

function RecordingTimer({ startedAt }: { startedAt: number }) {
  const [seconds, setSeconds] = useState(0)

  useEffect(() => {
    const timer = window.setInterval(() => {
      setSeconds(Math.max(0, Math.floor((Date.now() - startedAt) / 1000)))
    }, 250)
    return () => window.clearInterval(timer)
  }, [startedAt])

  const minutes = Math.floor(seconds / 60).toString().padStart(2, '0')
  const remainder = (seconds % 60).toString().padStart(2, '0')
  return <span className="font-mono">{minutes}:{remainder}</span>
}

export function ChatWindow({ wsRef }: ChatWindowProps) {
  const { messages, isInterviewing, connected } = useChatStore()
  const token = useAuthStore((state) => state.token)
  const [input, setInput] = useState('')
  const inputValueRef = useRef('')
  const answerInputRef = useRef<HTMLTextAreaElement>(null)
  const lastVoiceDraftRef = useRef<{ questionId: string; text: string } | null>(null)
  const [draftNotice, setDraftNotice] = useState<{ questionId: string; message: string } | null>(null)
  const [submittedQuestionId, setSubmittedQuestionId] = useState<string | null>(null)

  const handleDraftReady = useCallback((questionId: string, text: string) => {
    const latestQuestion = useChatStore.getState().messages
      .findLast((message) => message.messageType === 'question')
    if (latestQuestion?.questionId !== questionId) return

    const current = inputValueRef.current
    const previousDraft = lastVoiceDraftRef.current
    let next = text
    let message = '识别结果已写入输入框，请确认后发送'
    if (
      previousDraft?.questionId === questionId
      && current.trimEnd().endsWith(previousDraft.text.trim())
    ) {
      const currentWithoutPreviousDraft = current.trimEnd()
        .slice(0, -previousDraft.text.trim().length)
      next = `${currentWithoutPreviousDraft}${text}`
      message = '新的识别结果已替换上一版语音草稿，请确认后发送'
    } else if (current.trim()) {
      next = `${current.trimEnd()}\n${text}`
      message = '输入框已有内容，识别结果已追加到末尾，请确认后发送'
    }
    inputValueRef.current = next
    lastVoiceDraftRef.current = { questionId, text }
    setInput(next)
    setDraftNotice({ questionId, message })
    window.requestAnimationFrame(() => answerInputRef.current?.focus())
  }, [])

  const {
    state: speechState,
    available: speechAvailable,
    ttsAvailable,
    asrAvailable,
    enabled: speechEnabled,
    setEnabled: setSpeechEnabled,
    unlockAudio,
    speakQuestion,
    skipSpeaking,
    startRecording,
    stopRecording,
    cancelRecording,
  } = useInterviewSpeech(token, { onDraftReady: handleDraftReady })
  const [attachedFile, setAttachedFile] = useState<{ name: string; data: string } | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [showTrainingSetup, setShowTrainingSetup] = useState(false)
  const [learningGoalText, setLearningGoalText] = useState('')
  const [studentProfileText, setStudentProfileText] = useState('')
  const [learningGoalFile, setLearningGoalFile] = useState<{ name: string; data: string } | null>(null)
  const [studentProfileFile, setStudentProfileFile] = useState<{ name: string; data: string } | null>(null)
  const questionFileRef = useRef<HTMLInputElement>(null)
  const [uploadingQuestions, setUploadingQuestions] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const currentQuestion = messages.findLast((message) => message.messageType === 'question')

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  useEffect(() => {
    if (!currentQuestion) return
    void speakQuestion({
      questionId: currentQuestion.questionId || '',
      questionNum: currentQuestion.questionNum || 0,
      text: currentQuestion.speechText || '',
    })
  }, [currentQuestion, speakQuestion, speechAvailable])

  useEffect(() => {
    if (!isInterviewing || !connected) {
      skipSpeaking()
      cancelRecording()
    }
  }, [cancelRecording, connected, isInterviewing, skipSpeaking])

  const send = (msg: ClientMessage) => {
    wsRef.current?.send(msg)
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      const base64 = (reader.result as string).split(',')[1]
      setAttachedFile({ name: file.name, data: base64 })
    }
    reader.readAsDataURL(file)
    e.target.value = ''
  }

  const handleSend = () => {
    const text = input.trim()
    if (!text && !attachedFile) return

    if (isInterviewing) {
      cancelRecording()
      send({ type: 'answer', content: text })
      setSubmittedQuestionId(currentQuestion?.questionId || null)
    } else if (attachedFile) {
      // 带附件的消息：文件内容 + 文本一起发送
      const content = `[FILE:${attachedFile.name}]${attachedFile.data}` + (text ? `\n${text}` : '')
      send({ type: 'chat', content })
    } else {
      send({ type: 'chat', content: text })
    }

    const displayText = attachedFile
      ? `${attachedFile.name}${text ? '\n' + text : ''}`
      : text
    useChatStore.getState().addMessage({
      id: String(Date.now()),
      role: 'user',
      content: displayText,
      messageType: attachedFile ? 'file' : 'text',
      timestamp: Date.now(),
    })
    setInput('')
    inputValueRef.current = ''
    lastVoiceDraftRef.current = null
    setDraftNotice(null)
    setAttachedFile(null)
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handleUploadQuestions = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setUploadingQuestions(true)
    const reader = new FileReader()
    reader.onload = () => {
      const base64 = (reader.result as string).split(',')[1]
      send({ type: 'upload_questions', filename: file.name, data: base64 })
      useChatStore.getState().addMessage({
        id: String(Date.now()),
        role: 'user',
        content: `上传题库：${file.name}`,
        messageType: 'file',
        timestamp: Date.now(),
      })
      setUploadingQuestions(false)
    }
    reader.readAsDataURL(file)
    e.target.value = ''
  }

  const handleStartTraining = () => {
    const learningGoal = learningGoalFile
      ? `[FILE:${learningGoalFile.name}]${learningGoalFile.data}`
      : learningGoalText
    const studentProfile = studentProfileFile
      ? `[FILE:${studentProfileFile.name}]${studentProfileFile.data}`
      : studentProfileText
    if (!learningGoal || !studentProfile) return

    if (speechEnabled) void unlockAudio().catch(() => undefined)
    setSubmittedQuestionId(null)
    send({ type: 'start_training', learning_goal: learningGoal, student_profile: studentProfile })
    useChatStore.getState().setInterviewing(true)
    useChatStore.getState().addMessage({
      id: String(Date.now()),
      role: 'user',
      content: '开始学生能力提升训练',
      messageType: 'text',
      timestamp: Date.now(),
    })
    setShowTrainingSetup(false)
    setLearningGoalText('')
    setStudentProfileText('')
    setLearningGoalFile(null)
    setStudentProfileFile(null)
  }

  const handleQuitTraining = () => {
    skipSpeaking()
    cancelRecording()
    send({ type: 'quit_training' })
  }

  return (
    <div className="flex-1 flex flex-col h-screen">
      <StageIndicator />

      {/* 消息列表 */}
      <div className="flex-1 overflow-y-auto px-4 py-6">
        {messages.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full text-gray-400">
            <p className="text-lg mb-2">师练 AI</p>
            <p className="text-sm">围绕学习目标、学生画像与能力标准开展个性化训练</p>
          </div>
        )}
        {messages.map((msg) => (
          <MessageBubble key={msg.id} msg={msg} />
        ))}
        <div ref={bottomRef} />
      </div>

      {/* 学生能力训练准备面板 */}
      {showTrainingSetup && (
        <div className="px-4 py-4 border-t bg-gray-50">
          <div className="grid grid-cols-2 gap-4 mb-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">学习目标与能力标准</label>
              <FileUpload
                label="上传课程标准、考试大纲或能力要求"
                accept=".pdf,.txt,.docx,.md"
                onFileLoaded={(name, data) => setLearningGoalFile({ name, data })}
              />
              <textarea
                value={learningGoalText}
                onChange={(e) => setLearningGoalText(e.target.value)}
                placeholder="例如：掌握 Go 并发编程；重点训练原理理解、实践应用与问题分析……"
                rows={4}
                className="mt-2 w-full border rounded-lg p-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">学生画像</label>
              <FileUpload
                label="上传学习经历、项目作品或能力画像（PDF/DOCX）"
                accept=".pdf,.txt,.docx"
                onFileLoaded={(name, data) => setStudentProfileFile({ name, data })}
              />
              <textarea
                value={studentProfileText}
                onChange={(e) => setStudentProfileText(e.target.value)}
                placeholder="请填写学段、学科、授课/实习经历、试讲或教研经历；暂无经历可写‘师范生，尚无正式授课经历’。"
                rows={4}
                className="mt-2 w-full border rounded-lg p-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>
          {speechAvailable && (
            <label className="mb-4 flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={speechEnabled}
                onChange={(event) => void setSpeechEnabled(event.target.checked)}
                className="h-4 w-4 accent-blue-600"
              />
              启用语音训练（
              {ttsAvailable && asrAvailable
                ? 'AI 教研员朗读任务，并可使用语音作答'
                : ttsAvailable ? 'AI 教研员将朗读任务' : '可使用语音作答'}
              ）
            </label>
          )}
          <div className="flex justify-end gap-2">
            <button
              onClick={() => setShowTrainingSetup(false)}
              className="px-4 py-2 text-sm text-gray-600 hover:bg-gray-200 rounded-lg"
            >
              取消
            </button>
            <button
              onClick={handleStartTraining}
              disabled={(!learningGoalText && !learningGoalFile) || (!studentProfileText && !studentProfileFile)}
              className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              开始训练
            </button>
          </div>
        </div>
      )}

      {/* 输入区 */}
      <div className="border-t px-4 py-3">
        {isInterviewing && speechAvailable && (
          <div className="mb-2 flex items-center gap-2 text-xs text-gray-600" aria-live="polite">
            <button
              type="button"
              onClick={() => void setSpeechEnabled(!speechEnabled)}
              className="rounded-md border border-gray-200 px-2 py-1 hover:bg-gray-50"
              aria-pressed={speechEnabled}
            >
              {speechEnabled ? '语音已开启' : '语音已关闭'}
            </button>
            {speechState.kind === 'synthesizing' && <span>正在生成教研员语音…</span>}
            {speechState.kind === 'speaking' && <span>AI 教研员正在提问…</span>}
            {speechState.kind === 'requesting_mic' && <span>正在请求麦克风权限…</span>}
            {speechState.kind === 'connecting_asr' && (
              <span>{speechState.degraded ? '实时识别不可用，将在录音结束后识别' : '正在连接语音识别服务…'}</span>
            )}
            {speechState.kind === 'finalizing' && (
              <span>{speechState.degraded ? '实时识别已降级，正在生成最终文本…' : '正在生成最终文本…'}</span>
            )}
            {(speechState.kind === 'synthesizing' || speechState.kind === 'speaking') && (
              <button
                type="button"
                onClick={skipSpeaking}
                className="rounded-md bg-amber-50 px-2 py-1 text-amber-700 hover:bg-amber-100"
              >
                跳过朗读
              </button>
            )}
            {speechState.kind === 'warning' && (
              <>
                <span className="text-amber-700">{speechState.message}</span>
                {asrAvailable && (
                  <button
                    type="button"
                    onClick={() => {
                      cancelRecording()
                      answerInputRef.current?.focus()
                    }}
                    className="rounded-md bg-gray-100 px-2 py-1 text-gray-700 hover:bg-gray-200"
                  >
                    改用键盘
                  </button>
                )}
              </>
            )}
          </div>
        )}
        {(speechState.kind === 'recording' || speechState.kind === 'finalizing') && (
          <div className={`mb-2 rounded-lg border px-3 py-2 text-sm ${
            speechState.degraded
              ? 'border-amber-200 bg-amber-50 text-amber-900'
              : 'border-blue-100 bg-blue-50 text-blue-800'
          }`} aria-live="polite">
            <div className={`mb-1 text-xs font-medium ${speechState.degraded ? 'text-amber-700' : 'text-blue-600'}`}>
              {speechState.degraded
                ? '实时字幕暂时不可用，录音结束后将继续识别'
                : speechState.kind === 'recording' ? '实时识别' : '最终识别处理中'}
            </div>
            <div>{speechState.partial || (speechState.degraded
              ? '请继续回答；结束录音后会通过备用识别生成草稿。'
              : '请开始回答，识别文字会显示在这里…')}</div>
          </div>
        )}
        {speechState.kind === 'warning'
          && speechState.partial
          && currentQuestion?.questionId === speechState.questionId && (
          <div className="mb-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900" aria-live="polite">
            <div className="mb-1 text-xs font-medium text-amber-700">识别失败前的最后实时文本（仅供参考）</div>
            <div>{speechState.partial}</div>
          </div>
        )}
        {speechState.kind === 'draft_ready' && currentQuestion?.questionId === speechState.questionId && (
          <div className="mb-2 flex flex-wrap items-center gap-2 rounded-lg border border-green-100 bg-green-50 px-3 py-2 text-xs text-green-800" aria-live="polite">
            <span>
              {speechState.degraded && '已通过降级识别生成草稿。'}
              {draftNotice?.questionId === speechState.questionId ? draftNotice.message : '识别结果已写入输入框，请确认后发送'}
            </span>
            <button
              type="button"
              onClick={() => void startRecording(speechState.questionId)}
              className="rounded-md bg-white px-2 py-1 text-green-700 shadow-sm hover:bg-green-100"
            >
              重新录制
            </button>
            <button
              type="button"
              onClick={() => {
                cancelRecording()
                answerInputRef.current?.focus()
              }}
              className="rounded-md bg-white px-2 py-1 text-gray-700 shadow-sm hover:bg-gray-100"
            >
              改用键盘
            </button>
          </div>
        )}
        {/* 附件预览 */}
        {attachedFile && (
          <div className="flex items-center gap-2 mb-2 px-2 py-1.5 bg-blue-50 border border-blue-200 rounded-lg text-sm">
            <span className="text-blue-700">{attachedFile.name}</span>
            <button
              onClick={() => setAttachedFile(null)}
              className="text-gray-400 hover:text-red-500 ml-auto"
            >
              x
            </button>
          </div>
        )}
        <div className="flex items-end gap-2">
          {!isInterviewing && (
            <>
              <button
                onClick={() => setShowTrainingSetup(!showTrainingSetup)}
                className="px-3 py-2 text-sm bg-green-600 text-white rounded-lg hover:bg-green-700 whitespace-nowrap"
              >
                开始训练
              </button>
              <input
                ref={questionFileRef}
                type="file"
                accept=".pdf,.txt,.md,.docx"
                className="hidden"
                onChange={handleUploadQuestions}
              />
              <div className="relative group">
                <button
                  onClick={() => questionFileRef.current?.click()}
                  disabled={uploadingQuestions}
                  className="px-3 py-2 text-sm bg-purple-600 text-white rounded-lg hover:bg-purple-700 disabled:opacity-50 whitespace-nowrap"
                >
                  {uploadingQuestions ? '解析中...' : '上传题库'}
                </button>
                <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 w-64 px-3 py-2 bg-gray-800 text-white text-xs rounded-lg opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-50">
                  <p className="font-medium mb-1">上传能力训练题库</p>
                  <p>支持知识理解、实践应用与综合情境题，系统自动解析入库。</p>
                  <p className="mt-1 text-gray-300">• 不同文件名 → 追加到知识库</p>
                  <p className="text-gray-300">• 同文件名重传 → 自动更新该题库</p>
                  <p className="text-gray-300">• 相同文件内容 → 自动跳过</p>
                  <div className="absolute top-full left-1/2 -translate-x-1/2 border-4 border-transparent border-t-gray-800" />
                </div>
              </div>
            </>
          )}
          {isInterviewing && (
            <button
              onClick={handleQuitTraining}
              className="px-3 py-2 text-sm bg-red-500 text-white rounded-lg hover:bg-red-600 whitespace-nowrap"
            >
              终止训练
            </button>
          )}
          {isInterviewing
            && speechEnabled
            && asrAvailable
            && currentQuestion?.questionId
            && currentQuestion.questionId !== submittedQuestionId
            && speechState.kind !== 'draft_ready' && (
            <button
              type="button"
              onClick={() => {
                if (speechState.kind === 'recording') void stopRecording()
                else void startRecording(currentQuestion.questionId || '')
              }}
              disabled={
                speechState.kind === 'requesting_mic'
                || speechState.kind === 'connecting_asr'
                || speechState.kind === 'finalizing'
              }
              className={`px-3 py-2 text-sm text-white rounded-lg whitespace-nowrap disabled:cursor-wait disabled:opacity-60 ${
                speechState.kind === 'recording'
                  ? 'bg-amber-600 hover:bg-amber-700'
                  : 'bg-emerald-600 hover:bg-emerald-700'
              }`}
              aria-label={speechState.kind === 'recording' ? '结束语音识别' : '开始语音作答'}
            >
              {speechState.kind === 'recording' ? (
                <span className="flex items-center gap-1.5">
                  结束识别
                  <RecordingTimer key={speechState.startedAt} startedAt={speechState.startedAt} />
                </span>
              ) : speechState.kind === 'requesting_mic' ? '请求权限…'
                : speechState.kind === 'connecting_asr' ? '连接中…'
                  : speechState.kind === 'finalizing' ? '识别中…' : '语音作答'}
            </button>
          )}
          {/* 文件上传按钮 */}
          <input
            ref={fileInputRef}
            type="file"
            accept=".pdf,.txt,.docx,.md"
            className="hidden"
            onChange={handleFileSelect}
          />
          <button
            onClick={() => fileInputRef.current?.click()}
            className="p-2 text-gray-500 hover:text-gray-700 hover:bg-gray-100 rounded-lg transition"
            title="上传文件"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
            </svg>
          </button>
          <textarea
            ref={answerInputRef}
            value={input}
            onChange={(e) => {
              inputValueRef.current = e.target.value
              setInput(e.target.value)
            }}
            onKeyDown={handleKeyDown}
            placeholder={isInterviewing ? '说明你的判断、解题步骤和依据...' : '咨询学习问题，或点击“开始训练”进入完整训练...'}
            rows={1}
            className="flex-1 border rounded-xl px-4 py-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <button
            onClick={handleSend}
            disabled={!input.trim() && !attachedFile}
            className="px-4 py-2 bg-blue-600 text-white rounded-xl hover:bg-blue-700 disabled:opacity-50"
          >
            发送
          </button>
        </div>
      </div>
    </div>
  )
}
