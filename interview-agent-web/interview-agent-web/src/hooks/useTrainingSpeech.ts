import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ASRClientError,
  ASRWebSocketClient,
  getSpeechCapabilities,
  synthesizeQuestion,
} from '../api/speech'
import type { SpeakableQuestion, SpeechCapabilities, SpeechState } from '../types/speech'

interface UseTrainingSpeechOptions {
  onDraftReady?: (questionId: string, text: string) => void
}

interface UseTrainingSpeechResult {
  state: SpeechState
  capabilities: SpeechCapabilities | null
  available: boolean
  ttsAvailable: boolean
  asrAvailable: boolean
  enabled: boolean
  setEnabled: (enabled: boolean) => Promise<void>
  unlockAudio: () => Promise<void>
  speakQuestion: (question: SpeakableQuestion) => Promise<void>
  skipSpeaking: () => void
  startRecording: (questionId: string) => Promise<void>
  stopRecording: () => Promise<void>
  cancelRecording: () => void
  dispose: () => void
}

interface PCMWorkletMessage {
  type: 'pcm' | 'flushed'
  buffer?: ArrayBuffer
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function isASRActiveState(state: SpeechState): boolean {
  return state.kind === 'requesting_mic'
    || state.kind === 'connecting_asr'
    || state.kind === 'recording'
    || state.kind === 'finalizing'
}

function publicRecordingError(error: unknown): string {
  if (error instanceof ASRClientError) return error.message
  if (error instanceof DOMException) {
    if (error.name === 'NotAllowedError' || error.name === 'SecurityError') {
      return '没有麦克风权限，请在浏览器设置中允许后重试，或改用键盘输入'
    }
    if (error.name === 'NotFoundError' || error.name === 'DevicesNotFoundError') {
      return '未找到可用麦克风，请改用键盘输入'
    }
    if (error.name === 'NotReadableError' || error.name === 'TrackStartError') {
      return '麦克风正被其他程序占用，请关闭占用后重试'
    }
  }
  return '语音识别启动失败，请改用键盘输入'
}

export function useTrainingSpeech(
  token: string | null,
  options: UseTrainingSpeechOptions = {},
): UseTrainingSpeechResult {
  const [state, setState] = useState<SpeechState>({ kind: 'disabled' })
  const [capabilityResult, setCapabilityResult] = useState<{
    token: string
    value: SpeechCapabilities | null
  } | null>(null)
  const [enabledPreference, setEnabledPreference] = useState(true)

  const mountedRef = useRef(true)
  const stateRef = useRef<SpeechState>({ kind: 'disabled' })
  const capabilitiesRef = useRef<SpeechCapabilities | null>(null)
  const enabledPreferenceRef = useRef(true)
  const onDraftReadyRef = useRef(options.onDraftReady)
  const audioContextRef = useRef<AudioContext | null>(null)
  const workletModuleContextRef = useRef<AudioContext | null>(null)
  const sourceRef = useRef<AudioBufferSourceNode | null>(null)
  const playbackFinishRef = useRef<(() => void) | null>(null)
  const requestControllerRef = useRef<AbortController | null>(null)
  const playbackOperationRef = useRef(0)
  const asrOperationRef = useRef(0)
  const currentQuestionRef = useRef<string | null>(null)
  const spokenQuestionIdsRef = useRef(new Set<string>())

  const asrClientRef = useRef<ASRWebSocketClient | null>(null)
  const mediaStreamRef = useRef<MediaStream | null>(null)
  const mediaSourceRef = useRef<MediaStreamAudioSourceNode | null>(null)
  const workletNodeRef = useRef<AudioWorkletNode | null>(null)
  const muteGainRef = useRef<GainNode | null>(null)
  const acceptAudioRef = useRef(false)
  const asrDegradedRef = useRef(false)
  const flushResolverRef = useRef<(() => void) | null>(null)
  const maxRecordingTimerRef = useRef<number | null>(null)
  const stopRecordingRef = useRef<() => Promise<void>>(async () => undefined)

  const capabilities = capabilityResult?.token === token ? capabilityResult.value : null
  const ttsAvailable = Boolean(capabilities?.enabled && capabilities.tts_enabled)
  const asrAvailable = Boolean(capabilities?.enabled && capabilities.asr_enabled)
  const available = ttsAvailable || asrAvailable
  const enabled = available && enabledPreference

  const transition = useCallback((nextState: SpeechState) => {
    stateRef.current = nextState
    if (mountedRef.current) setState(nextState)
  }, [])

  const ensureAudioContext = useCallback((): AudioContext => {
    let context = audioContextRef.current
    if (!context || context.state === 'closed') {
      context = new AudioContext()
      audioContextRef.current = context
      workletModuleContextRef.current = null
    }
    return context
  }, [])

  const setRestingState = useCallback(() => {
    const currentCapabilities = capabilitiesRef.current
    const canUseSpeech = Boolean(
      enabledPreferenceRef.current
      && currentCapabilities?.enabled
      && (currentCapabilities.tts_enabled || currentCapabilities.asr_enabled),
    )
    transition(canUseSpeech ? { kind: 'idle' } : { kind: 'disabled' })
  }, [transition])

  const cancelPlayback = useCallback((resetState: boolean) => {
    playbackOperationRef.current += 1
    requestControllerRef.current?.abort()
    requestControllerRef.current = null

    const source = sourceRef.current
    sourceRef.current = null
    if (source) {
      source.onended = null
      try {
        source.stop()
      } catch {
        // A source that already ended cannot be stopped again.
      }
      source.disconnect()
    }

    const finishPlayback = playbackFinishRef.current
    playbackFinishRef.current = null
    finishPlayback?.()

    if (resetState && !isASRActiveState(stateRef.current)) setRestingState()
  }, [setRestingState])

  const releaseMicrophone = useCallback(() => {
    acceptAudioRef.current = false
    if (maxRecordingTimerRef.current !== null) {
      window.clearTimeout(maxRecordingTimerRef.current)
      maxRecordingTimerRef.current = null
    }
    const finishFlush = flushResolverRef.current
    flushResolverRef.current = null
    finishFlush?.()

    const worklet = workletNodeRef.current
    workletNodeRef.current = null
    if (worklet) {
      worklet.port.onmessage = null
      worklet.disconnect()
    }
    const source = mediaSourceRef.current
    mediaSourceRef.current = null
    source?.disconnect()
    const gain = muteGainRef.current
    muteGainRef.current = null
    gain?.disconnect()

    const stream = mediaStreamRef.current
    mediaStreamRef.current = null
    stream?.getTracks().forEach((track) => track.stop())
  }, [])

  const releaseRecordingResources = useCallback(() => {
    releaseMicrophone()
    const client = asrClientRef.current
    asrClientRef.current = null
    client?.cancel()
    asrDegradedRef.current = false
  }, [releaseMicrophone])

  const cancelRecording = useCallback(() => {
    asrOperationRef.current += 1
    releaseRecordingResources()
    asrDegradedRef.current = false
    if (
      isASRActiveState(stateRef.current)
      || stateRef.current.kind === 'draft_ready'
      || stateRef.current.kind === 'warning'
    ) {
      setRestingState()
    }
  }, [releaseRecordingResources, setRestingState])

  const unlockAudio = useCallback(async () => {
    const currentCapabilities = capabilitiesRef.current
    if (
      !enabledPreferenceRef.current
      || !currentCapabilities?.enabled
      || (!currentCapabilities.tts_enabled && !currentCapabilities.asr_enabled)
    ) return
    const context = ensureAudioContext()
    if (context.state === 'suspended') await context.resume()
  }, [ensureAudioContext])

  const setEnabled = useCallback(async (nextEnabled: boolean) => {
    enabledPreferenceRef.current = nextEnabled
    setEnabledPreference(nextEnabled)
    if (!nextEnabled) {
      cancelPlayback(false)
      asrOperationRef.current += 1
      releaseRecordingResources()
      transition({ kind: 'disabled' })
      return
    }

    const currentCapabilities = capabilitiesRef.current
    if (
      !currentCapabilities?.enabled
      || (!currentCapabilities.tts_enabled && !currentCapabilities.asr_enabled)
    ) {
      transition({ kind: 'disabled' })
      return
    }
    try {
      await unlockAudio()
      setRestingState()
    } catch {
      transition({ kind: 'warning', message: '浏览器暂时无法初始化语音功能，请继续使用文字训练', recoverTo: 'idle' })
    }
  }, [cancelPlayback, releaseRecordingResources, setRestingState, transition, unlockAudio])

  const speakQuestion = useCallback(async (question: SpeakableQuestion) => {
    if (currentQuestionRef.current !== question.questionId) {
      cancelPlayback(false)
      asrOperationRef.current += 1
      releaseRecordingResources()
      currentQuestionRef.current = question.questionId
    }
    if (spokenQuestionIdsRef.current.has(question.questionId)) return

    const currentCapabilities = capabilitiesRef.current
    if (!question.questionId || question.questionNum <= 0 || !question.text.trim()) {
      spokenQuestionIdsRef.current.add(question.questionId)
      setRestingState()
      return
    }
    // Capabilities may still be loading when the first question arrives. Do
    // not mark it as spoken until the capability result is known.
    if (!currentCapabilities) {
      setRestingState()
      return
    }
    if (
      !enabledPreferenceRef.current
      || !currentCapabilities.enabled
      || !currentCapabilities.tts_enabled
      || !token
    ) {
      spokenQuestionIdsRef.current.add(question.questionId)
      setRestingState()
      return
    }
    spokenQuestionIdsRef.current.add(question.questionId)

    const operation = ++playbackOperationRef.current
    const controller = new AbortController()
    requestControllerRef.current = controller
    transition({ kind: 'synthesizing', questionId: question.questionId })

    try {
      const audio = await synthesizeQuestion(token, question, controller.signal)
      const audioBytes = await audio.arrayBuffer()
      if (
        controller.signal.aborted
        || operation !== playbackOperationRef.current
        || currentQuestionRef.current !== question.questionId
        || !mountedRef.current
      ) return

      const context = ensureAudioContext()
      if (context.state === 'suspended') await context.resume()
      const decodedAudio = await context.decodeAudioData(audioBytes)
      if (
        controller.signal.aborted
        || operation !== playbackOperationRef.current
        || currentQuestionRef.current !== question.questionId
        || !mountedRef.current
      ) return

      const source = context.createBufferSource()
      source.buffer = decodedAudio
      source.connect(context.destination)
      sourceRef.current = source
      transition({ kind: 'speaking', questionId: question.questionId })

      await new Promise<void>((resolve) => {
        let settled = false
        const finish = () => {
          if (settled) return
          settled = true
          resolve()
        }
        playbackFinishRef.current = finish
        source.onended = finish
        source.start()
      })

      if (sourceRef.current === source) {
        sourceRef.current = null
        source.onended = null
        source.disconnect()
      }
      playbackFinishRef.current = null
      if (
        operation === playbackOperationRef.current
        && currentQuestionRef.current === question.questionId
        && mountedRef.current
      ) setRestingState()
    } catch (error) {
      if (controller.signal.aborted || operation !== playbackOperationRef.current || isAbortError(error)) return
      cancelPlayback(false)
      transition({ kind: 'warning', message: '语音播放失败，请继续使用文字作答', recoverTo: 'idle' })
    } finally {
      if (requestControllerRef.current === controller) requestControllerRef.current = null
    }
  }, [cancelPlayback, ensureAudioContext, releaseRecordingResources, setRestingState, token, transition])

  const skipSpeaking = useCallback(() => {
    cancelPlayback(true)
  }, [cancelPlayback])

  const startRecording = useCallback(async (questionId: string) => {
    const currentCapabilities = capabilitiesRef.current
    if (
      !token
      || !questionId
      || questionId !== currentQuestionRef.current
      || !enabledPreferenceRef.current
      || !currentCapabilities?.enabled
      || !currentCapabilities.asr_enabled
      || currentCapabilities.input_format !== 'pcm_s16le'
      || currentCapabilities.input_sample_rate !== 16000
    ) {
      transition({ kind: 'warning', message: '当前问题暂时不能使用语音作答，请改用键盘输入', recoverTo: 'idle' })
      return
    }

    cancelPlayback(false)
    asrOperationRef.current += 1
    releaseRecordingResources()
    const operation = asrOperationRef.current
    transition({ kind: 'requesting_mic', questionId })

    const isCurrentOperation = () => operation === asrOperationRef.current
      && currentQuestionRef.current === questionId
      && mountedRef.current

    const failRecording = (error: unknown) => {
      if (!isCurrentOperation()) return
      const current = stateRef.current
      const partial = (current.kind === 'recording' || current.kind === 'finalizing')
        && current.questionId === questionId
        ? current.partial.trim()
        : ''
      asrOperationRef.current += 1
      releaseRecordingResources()
      transition({
        kind: 'warning',
        message: publicRecordingError(error),
        recoverTo: 'idle',
        questionId,
        partial: partial || undefined,
      })
    }

    let pendingStream: MediaStream | null = null
    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        throw new ASRClientError('当前浏览器不支持麦克风录音，请改用键盘输入', 'MIC_UNSUPPORTED')
      }
      pendingStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
        video: false,
      })
      if (!isCurrentOperation()) {
        pendingStream.getTracks().forEach((track) => track.stop())
        return
      }
      mediaStreamRef.current = pendingStream
      pendingStream = null

      const context = ensureAudioContext()
      if (context.state === 'suspended') await context.resume()
      if (workletModuleContextRef.current !== context) {
        await context.audioWorklet.addModule('/pcm-worklet.js')
        workletModuleContextRef.current = context
      }
      if (!isCurrentOperation()) {
        releaseRecordingResources()
        return
      }

      transition({ kind: 'connecting_asr', questionId, degraded: false })
      const client = new ASRWebSocketClient(
        token,
        questionId,
        { format: 'pcm_s16le', sampleRate: 16000, channels: 1 },
        {
          onPartial: (message) => {
            if (!isCurrentOperation()) return
            const current = stateRef.current
            if (current.kind === 'recording' && current.questionId === questionId) {
              transition({ ...current, partial: message.text })
            } else if (current.kind === 'finalizing' && current.questionId === questionId) {
              transition({ ...current, partial: message.text })
            }
          },
          onWarning: () => {
            if (!isCurrentOperation()) return
            asrDegradedRef.current = true
            const current = stateRef.current
            if (
              (current.kind === 'connecting_asr'
                || current.kind === 'recording'
                || current.kind === 'finalizing')
              && current.questionId === questionId
            ) {
              transition({ ...current, degraded: true })
            }
          },
          onFinal: (message) => {
            if (!isCurrentOperation()) return
            const text = message.text.trim()
            const degraded = Boolean(message.degraded || asrDegradedRef.current)
            asrOperationRef.current += 1
            releaseRecordingResources()
            if (!text) {
              transition({ kind: 'warning', message: '没有识别到有效回答，请重新录制或改用键盘输入', recoverTo: 'idle' })
              return
            }
            transition({
              kind: 'draft_ready',
              questionId,
              text,
              degraded,
            })
            onDraftReadyRef.current?.(questionId, text)
          },
          onError: failRecording,
        },
      )
      asrClientRef.current = client
      await client.connect()
      if (!isCurrentOperation()) {
        releaseRecordingResources()
        return
      }

      const stream = mediaStreamRef.current
      if (!stream) throw new ASRClientError('麦克风连接已关闭，请重新录制', 'MIC_CLOSED')
      const source = context.createMediaStreamSource(stream)
      const worklet = new AudioWorkletNode(context, 'training-pcm-capture', {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
        channelCount: 1,
      })
      const muteGain = context.createGain()
      muteGain.gain.value = 0
      mediaSourceRef.current = source
      workletNodeRef.current = worklet
      muteGainRef.current = muteGain
      acceptAudioRef.current = true

      worklet.port.onmessage = (event: MessageEvent<PCMWorkletMessage>) => {
        if (!isCurrentOperation()) return
        if (event.data?.type === 'flushed') {
          const finishFlush = flushResolverRef.current
          flushResolverRef.current = null
          finishFlush?.()
          return
        }
        if (event.data?.type !== 'pcm' || !event.data.buffer || !acceptAudioRef.current) return
        try {
          client.sendAudio(event.data.buffer)
        } catch (error) {
          failRecording(error)
        }
      }
      source.connect(worklet)
      worklet.connect(muteGain)
      muteGain.connect(context.destination)
      transition({
        kind: 'recording',
        questionId,
        partial: '',
        startedAt: Date.now(),
        degraded: asrDegradedRef.current,
      })
      maxRecordingTimerRef.current = window.setTimeout(() => {
        void stopRecordingRef.current()
      }, Math.max(1, currentCapabilities.max_answer_seconds) * 1000)
    } catch (error) {
      pendingStream?.getTracks().forEach((track) => track.stop())
      failRecording(error)
    }
  }, [cancelPlayback, ensureAudioContext, releaseRecordingResources, token, transition])

  const stopRecording = useCallback(async () => {
    const current = stateRef.current
    if (current.kind !== 'recording') return
    const operation = asrOperationRef.current
    transition({
      kind: 'finalizing',
      questionId: current.questionId,
      partial: current.partial,
      degraded: current.degraded,
    })

    const worklet = workletNodeRef.current
    if (worklet) {
      await new Promise<void>((resolve) => {
        let settled = false
        const finish = () => {
          if (settled) return
          settled = true
          window.clearTimeout(timeout)
          if (flushResolverRef.current === finish) flushResolverRef.current = null
          resolve()
        }
        const timeout = window.setTimeout(finish, 500)
        flushResolverRef.current = finish
        worklet.port.postMessage({ type: 'stop' })
      })
    }
    if (operation !== asrOperationRef.current || current.questionId !== currentQuestionRef.current) return

    acceptAudioRef.current = false
    releaseMicrophone()
    try {
      asrClientRef.current?.stop()
    } catch (error) {
      asrOperationRef.current += 1
      releaseRecordingResources()
      transition({ kind: 'warning', message: publicRecordingError(error), recoverTo: 'idle' })
    }
  }, [releaseMicrophone, releaseRecordingResources, transition])

  useEffect(() => {
    stopRecordingRef.current = stopRecording
  }, [stopRecording])

  const dispose = useCallback(() => {
    cancelPlayback(false)
    asrOperationRef.current += 1
    releaseRecordingResources()
    currentQuestionRef.current = null
    spokenQuestionIdsRef.current.clear()
    const context = audioContextRef.current
    audioContextRef.current = null
    workletModuleContextRef.current = null
    if (context && context.state !== 'closed') void context.close().catch(() => undefined)
  }, [cancelPlayback, releaseRecordingResources])

  useEffect(() => {
    onDraftReadyRef.current = options.onDraftReady
  }, [options.onDraftReady])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      dispose()
    }
  }, [dispose])

  useEffect(() => {
    cancelPlayback(false)
    asrOperationRef.current += 1
    releaseRecordingResources()
    capabilitiesRef.current = null
    if (!token) {
      transition({ kind: 'disabled' })
      return
    }

    const controller = new AbortController()
    let active = true
    void getSpeechCapabilities(token, controller.signal)
      .then((nextCapabilities) => {
        if (!active || !mountedRef.current) return
        capabilitiesRef.current = nextCapabilities
        setCapabilityResult({ token, value: nextCapabilities })
        setRestingState()
      })
      .catch((error: unknown) => {
        if (!active || isAbortError(error) || !mountedRef.current) return
        capabilitiesRef.current = null
        setCapabilityResult({ token, value: null })
        transition({ kind: 'disabled' })
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [cancelPlayback, releaseRecordingResources, setRestingState, token, transition])

  return {
    state,
    capabilities,
    available,
    ttsAvailable,
    asrAvailable,
    enabled,
    setEnabled,
    unlockAudio,
    speakQuestion,
    skipSpeaking,
    startRecording,
    stopRecording,
    cancelRecording,
    dispose,
  }
}
