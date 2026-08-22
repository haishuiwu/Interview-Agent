import type {
  ASRInputConfig,
  ASRServerMessage,
  SpeakableQuestion,
  SpeechCapabilities,
} from '../types/speech'

const SPEECH_API_BASE = '/api/speech'
const MAX_ASR_BUFFERED_BYTES = 1024 * 1024
const MAX_ASR_PCM_FRAME_BYTES = 64 * 1024

interface SpeechErrorPayload {
  error?: {
    code?: string
    message?: string
  }
  request_id?: string
}

export class SpeechAPIError extends Error {
  readonly code: string
  readonly status: number
  readonly requestId?: string

  constructor(message: string, code: string, status: number, requestId?: string) {
    super(message)
    this.name = 'SpeechAPIError'
    this.code = code
    this.status = status
    this.requestId = requestId
  }
}

function authorizationHeaders(token: string): HeadersInit {
  return { Authorization: `Bearer ${token}` }
}

async function responseError(response: Response): Promise<SpeechAPIError> {
  let payload: SpeechErrorPayload = {}
  try {
    payload = await response.json() as SpeechErrorPayload
  } catch {
    // The public fallback intentionally does not expose an upstream response body.
  }
  return new SpeechAPIError(
    payload.error?.message || '语音服务暂时不可用，请继续使用文字训练',
    payload.error?.code || 'SPEECH_REQUEST_FAILED',
    response.status,
    payload.request_id,
  )
}

function isSpeechCapabilities(value: unknown): value is SpeechCapabilities {
  if (!value || typeof value !== 'object') return false
  const item = value as Record<string, unknown>
  return typeof item.enabled === 'boolean'
    && typeof item.tts_enabled === 'boolean'
    && typeof item.asr_enabled === 'boolean'
    && typeof item.max_answer_seconds === 'number'
    && typeof item.input_format === 'string'
    && typeof item.input_sample_rate === 'number'
}

export async function getSpeechCapabilities(token: string, signal?: AbortSignal): Promise<SpeechCapabilities> {
  const response = await fetch(`${SPEECH_API_BASE}/capabilities`, {
    headers: {
      ...authorizationHeaders(token),
      Accept: 'application/json',
    },
    signal,
  })
  if (!response.ok) throw await responseError(response)

  const payload: unknown = await response.json()
  if (!isSpeechCapabilities(payload)) {
    throw new SpeechAPIError('语音能力响应无效，请继续使用文字训练', 'INVALID_CAPABILITIES', response.status)
  }
  return payload
}

export async function synthesizeQuestion(
  token: string,
  question: SpeakableQuestion,
  signal?: AbortSignal,
): Promise<Blob> {
  const response = await fetch(`${SPEECH_API_BASE}/tts`, {
    method: 'POST',
    headers: {
      ...authorizationHeaders(token),
      Accept: 'audio/wav',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      question_id: question.questionId,
      text: question.text,
    }),
    signal,
  })
  if (!response.ok) throw await responseError(response)

  const contentType = response.headers.get('Content-Type')?.split(';', 1)[0].trim().toLowerCase()
  if (contentType !== 'audio/wav') {
    throw new SpeechAPIError('语音响应格式无效，请继续使用文字训练', 'INVALID_TTS_AUDIO', response.status)
  }
  const audio = await response.blob()
  if (audio.size === 0) {
    throw new SpeechAPIError('语音响应为空，请继续使用文字训练', 'EMPTY_TTS_AUDIO', response.status)
  }
  return audio
}

export class ASRClientError extends Error {
  readonly code: string
  readonly retryable: boolean

  constructor(message: string, code: string, retryable = false) {
    super(message)
    this.name = 'ASRClientError'
    this.code = code
    this.retryable = retryable
  }
}

export interface ASRClientCallbacks {
  onPartial: (message: Extract<ASRServerMessage, { type: 'asr.partial' }>) => void
  onWarning: (message: Extract<ASRServerMessage, { type: 'asr.warning' }>) => void
  onFinal: (message: Extract<ASRServerMessage, { type: 'asr.final' }>) => void
  onError: (error: ASRClientError) => void
}

type ASRClientState = 'idle' | 'connecting' | 'ready' | 'streaming' | 'finalizing' | 'terminal' | 'closed'

function asrWebSocketURL(token: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/ws/speech/asr?token=${encodeURIComponent(token)}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

function parseASRServerMessage(data: unknown): ASRServerMessage {
  if (typeof data !== 'string') {
    throw new ASRClientError('语音识别响应格式无效，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR')
  }
  let value: unknown
  try {
    value = JSON.parse(data)
  } catch {
    throw new ASRClientError('语音识别响应格式无效，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR')
  }
  if (!isRecord(value) || typeof value.type !== 'string') {
    throw new ASRClientError('语音识别响应格式无效，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR')
  }

  switch (value.type) {
    case 'asr.ready':
      if (typeof value.question_id === 'string' && typeof value.request_id === 'string') {
        return value as unknown as Extract<ASRServerMessage, { type: 'asr.ready' }>
      }
      break
    case 'asr.partial':
      if (
        typeof value.question_id === 'string'
        && Number.isSafeInteger(value.seq)
        && (value.seq as number) > 0
        && typeof value.text === 'string'
      ) {
        return value as unknown as Extract<ASRServerMessage, { type: 'asr.partial' }>
      }
      break
    case 'asr.warning':
      if (
        typeof value.question_id === 'string'
        && value.code === 'REALTIME_DEGRADED'
        && typeof value.message === 'string'
      ) {
        return value as unknown as Extract<ASRServerMessage, { type: 'asr.warning' }>
      }
      break
    case 'asr.final':
      if (
        typeof value.question_id === 'string'
        && typeof value.text === 'string'
        && (value.degraded === undefined || typeof value.degraded === 'boolean')
        && (value.provider_request_id === undefined || typeof value.provider_request_id === 'string')
      ) {
        return value as unknown as Extract<ASRServerMessage, { type: 'asr.final' }>
      }
      break
    case 'asr.error':
      if (
        (value.question_id === undefined || typeof value.question_id === 'string')
        && typeof value.code === 'string'
        && typeof value.message === 'string'
        && (value.retryable === undefined || typeof value.retryable === 'boolean')
      ) {
        return value as unknown as Extract<ASRServerMessage, { type: 'asr.error' }>
      }
      break
  }
  throw new ASRClientError('语音识别响应格式无效，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR')
}

// ASRWebSocketClient owns the provider-neutral browser protocol only. React
// state, microphone capture and AudioWorklet resources remain in the hook.
export class ASRWebSocketClient {
  private readonly token: string
  private readonly questionId: string
  private readonly input: ASRInputConfig
  private readonly callbacks: ASRClientCallbacks
  private socket: WebSocket | null = null
  private state: ASRClientState = 'idle'
  private expectedClose = false
  private lastPartialSeq = 0
  private connectResolve: (() => void) | null = null
  private connectReject: ((error: ASRClientError) => void) | null = null

  constructor(token: string, questionId: string, input: ASRInputConfig, callbacks: ASRClientCallbacks) {
    this.token = token
    this.questionId = questionId
    this.input = input
    this.callbacks = callbacks
  }

  connect(): Promise<void> {
    if (this.state !== 'idle' || !this.token || !this.questionId) {
      return Promise.reject(new ASRClientError('语音识别请求无效', 'INVALID_REQUEST'))
    }
    this.state = 'connecting'

    return new Promise<void>((resolve, reject) => {
      this.connectResolve = resolve
      this.connectReject = reject
      let socket: WebSocket
      try {
        socket = new WebSocket(asrWebSocketURL(this.token))
      } catch {
        this.rejectConnect(new ASRClientError('无法连接语音识别服务，请改用键盘输入', 'UPSTREAM_UNAVAILABLE', true))
        return
      }
      this.socket = socket

      socket.onopen = () => {
        if (this.state !== 'connecting') return
        socket.send(JSON.stringify({
          type: 'asr.start',
          question_id: this.questionId,
          format: this.input.format,
          sample_rate: this.input.sampleRate,
          channels: this.input.channels,
        }))
      }
      socket.onmessage = (event) => this.handleMessage(event.data)
      socket.onerror = () => {
        // Browsers expose no safe error detail here; onclose performs the
        // single public failure transition without leaking transport data.
      }
      socket.onclose = () => this.handleClose()
    })
  }

  sendAudio(pcm: ArrayBuffer): void {
    const socket = this.socket
    if (!socket || socket.readyState !== WebSocket.OPEN || (this.state !== 'ready' && this.state !== 'streaming')) {
      throw new ASRClientError('语音识别连接尚未就绪', 'INVALID_REQUEST')
    }
    if (pcm.byteLength === 0 || pcm.byteLength > MAX_ASR_PCM_FRAME_BYTES || pcm.byteLength % 2 !== 0) {
      throw new ASRClientError('麦克风音频格式无效', 'INVALID_REQUEST')
    }
    if (socket.bufferedAmount > MAX_ASR_BUFFERED_BYTES) {
      throw new ASRClientError('网络发送过慢，已停止录音，请改用键盘输入', 'ASR_BACKPRESSURE', true)
    }
    socket.send(pcm)
    this.state = 'streaming'
  }

  stop(): void {
    const socket = this.socket
    if (!socket || socket.readyState !== WebSocket.OPEN || (this.state !== 'ready' && this.state !== 'streaming')) {
      throw new ASRClientError('当前录音无法停止', 'INVALID_REQUEST')
    }
    socket.send(JSON.stringify({ type: 'asr.stop' }))
    this.state = 'finalizing'
  }

  cancel(): void {
    if (this.state === 'closed') return
    const wasConnecting = this.state === 'connecting'
    this.expectedClose = true
    const socket = this.socket
    if (socket?.readyState === WebSocket.OPEN && this.state !== 'idle' && this.state !== 'terminal') {
      try {
        socket.send(JSON.stringify({ type: 'asr.cancel' }))
      } catch {
        // Closing the socket still cancels the server-side context.
      }
    }
    this.state = 'closed'
    if (wasConnecting) {
      this.rejectConnect(new ASRClientError('语音识别已取消', 'CLIENT_CANCELLED'))
    }
    socket?.close(1000, 'cancelled')
    this.clearSocketHandlersAfterClose(socket)
  }

  private handleMessage(data: unknown): void {
    if (this.state === 'closed' || this.state === 'terminal') return
    let message: ASRServerMessage
    try {
      message = parseASRServerMessage(data)
    } catch (error) {
      this.fail(error instanceof ASRClientError
        ? error
        : new ASRClientError('语音识别响应格式无效，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR'))
      return
    }
    if ('question_id' in message && message.question_id && message.question_id !== this.questionId) {
      this.fail(new ASRClientError('收到过期问题的语音识别结果，已忽略', 'STALE_QUESTION'))
      return
    }

    switch (message.type) {
      case 'asr.ready':
        if (this.state !== 'connecting') {
          this.fail(new ASRClientError('语音识别状态异常，请重新录制', 'UPSTREAM_PROTOCOL_ERROR'))
          return
        }
        this.state = 'ready'
        this.resolveConnect()
        return
      case 'asr.partial':
        if (
          (this.state === 'ready' || this.state === 'streaming' || this.state === 'finalizing')
          && message.seq > this.lastPartialSeq
          && message.text.trim()
        ) {
          this.lastPartialSeq = message.seq
          this.callbacks.onPartial(message)
        }
        return
      case 'asr.warning':
        if (this.state !== 'ready' && this.state !== 'streaming' && this.state !== 'finalizing') {
          this.fail(new ASRClientError('语音识别降级状态异常，请改用键盘输入', 'UPSTREAM_PROTOCOL_ERROR'))
          return
        }
        this.callbacks.onWarning(message)
        return
      case 'asr.final':
        // The server also finalizes automatically at the configured PCM cap,
        // so a valid final may arrive while the browser still says streaming.
        if (this.state !== 'ready' && this.state !== 'streaming' && this.state !== 'finalizing') {
          this.fail(new ASRClientError('语音识别结果状态异常，请重新录制', 'UPSTREAM_PROTOCOL_ERROR'))
          return
        }
        this.state = 'terminal'
        this.expectedClose = true
        this.callbacks.onFinal(message)
        return
      case 'asr.error': {
        const error = new ASRClientError(message.message, message.code, Boolean(message.retryable))
        this.state = 'terminal'
        this.expectedClose = true
        if (this.connectReject) this.rejectConnect(error)
        else this.callbacks.onError(error)
        this.socket?.close()
        return
      }
    }
  }

  private handleClose(): void {
    const previousState = this.state
    const expected = this.expectedClose || previousState === 'terminal' || previousState === 'closed'
    this.socket = null
    this.state = 'closed'
    if (this.connectReject) {
      this.rejectConnect(new ASRClientError(
        expected ? '语音识别已取消' : '语音识别连接已断开，请改用键盘输入',
        expected ? 'CLIENT_CANCELLED' : 'UPSTREAM_UNAVAILABLE',
        !expected,
      ))
      return
    }
    if (!expected) {
      this.callbacks.onError(new ASRClientError('语音识别连接已断开，请改用键盘输入', 'UPSTREAM_UNAVAILABLE', true))
    }
  }

  private fail(error: ASRClientError): void {
    if (this.state === 'terminal' || this.state === 'closed') return
    this.state = 'terminal'
    this.expectedClose = true
    if (this.connectReject) this.rejectConnect(error)
    else this.callbacks.onError(error)
    this.socket?.close()
  }

  private resolveConnect(): void {
    const resolve = this.connectResolve
    this.connectResolve = null
    this.connectReject = null
    resolve?.()
  }

  private rejectConnect(error: ASRClientError): void {
    const reject = this.connectReject
    this.connectResolve = null
    this.connectReject = null
    reject?.(error)
  }

  private clearSocketHandlersAfterClose(socket: WebSocket | null): void {
    if (!socket || socket.readyState !== WebSocket.CLOSED) return
    socket.onopen = null
    socket.onmessage = null
    socket.onerror = null
    socket.onclose = null
    if (this.socket === socket) this.socket = null
  }
}
