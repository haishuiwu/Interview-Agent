export interface SpeechCapabilities {
  enabled: boolean
  tts_enabled: boolean
  asr_enabled: boolean
  max_answer_seconds: number
  input_format: string
  input_sample_rate: number
}

export interface SpeakableQuestion {
  questionId: string
  questionNum: number
  text: string
}

export interface ASRInputConfig {
  format: 'pcm_s16le'
  sampleRate: 16000
  channels: 1
}

export type ASRServerMessage =
  | { type: 'asr.ready'; question_id: string; request_id: string }
  | { type: 'asr.partial'; question_id: string; seq: number; text: string }
  | {
    type: 'asr.warning'
    question_id: string
    code: 'REALTIME_DEGRADED'
    message: string
  }
  | {
    type: 'asr.final'
    question_id: string
    text: string
    degraded?: boolean
    provider_request_id?: string
  }
  | {
    type: 'asr.error'
    question_id?: string
    code: string
    message: string
    retryable?: boolean
  }

export type SpeechState =
  | { kind: 'disabled' }
  | { kind: 'idle' }
  | { kind: 'synthesizing'; questionId: string }
  | { kind: 'speaking'; questionId: string }
  | { kind: 'requesting_mic'; questionId: string }
  | { kind: 'connecting_asr'; questionId: string; degraded: boolean }
  | { kind: 'recording'; questionId: string; partial: string; startedAt: number; degraded: boolean }
  | { kind: 'finalizing'; questionId: string; partial: string; degraded: boolean }
  | { kind: 'draft_ready'; questionId: string; text: string; degraded: boolean }
  | {
    kind: 'warning'
    message: string
    recoverTo: 'idle' | 'draft_ready'
    questionId?: string
    partial?: string
  }
