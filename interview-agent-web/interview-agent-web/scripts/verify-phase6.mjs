import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import vm from 'node:vm'

const workletSource = await readFile(new URL('../public/pcm-worklet.js', import.meta.url), 'utf8')
const asrClientSource = await readFile(new URL('../src/api/speech.ts', import.meta.url), 'utf8')
const speechHookSource = await readFile(new URL('../src/hooks/useInterviewSpeech.ts', import.meta.url), 'utf8')
const chatWindowSource = await readFile(new URL('../src/components/ChatWindow.tsx', import.meta.url), 'utf8')

assert.doesNotMatch(workletSource, /\b(?:fetch|WebSocket|XMLHttpRequest)\b/, 'Worklet must not access the network')
assert.doesNotMatch(workletSource, /ScriptProcessorNode/, 'Deprecated ScriptProcessorNode must not be used')
assert.match(asrClientSource, /\/ws\/speech\/asr\?token=/, 'ASR client must use the Phase 5 endpoint')
assert.match(asrClientSource, /MAX_ASR_BUFFERED_BYTES = 1024 \* 1024/, 'ASR client must enforce 1 MiB backpressure')
for (const controlType of ['asr.start', 'asr.stop', 'asr.cancel']) {
  assert.ok(asrClientSource.includes(controlType), `ASR client is missing ${controlType}`)
}
assert.match(speechHookSource, /getTracks\(\)\.forEach\(\(track\) => track\.stop\(\)\)/, 'All microphone tracks must stop')
assert.match(speechHookSource, /currentQuestionRef\.current === questionId/, 'ASR callbacks must validate current question ID')
assert.match(chatWindowSource, /latestQuestion\?\.questionId !== questionId/, 'Draft writes must validate the latest store question')

function runWorklet(sourceRate) {
  let Processor
  class FakePort {
    constructor() {
      this.onmessage = null
      this.messages = []
    }

    postMessage(message) {
      this.messages.push(message)
    }
  }
  class FakeAudioWorkletProcessor {
    constructor() {
      this.port = new FakePort()
    }
  }

  vm.runInNewContext(workletSource, {
    AudioWorkletProcessor: FakeAudioWorkletProcessor,
    ArrayBuffer,
    DataView,
    Math,
    sampleRate: sourceRate,
    registerProcessor(name, constructor) {
      assert.equal(name, 'interview-pcm-capture')
      Processor = constructor
    },
  }, { filename: 'pcm-worklet.js' })

  assert.ok(Processor, 'Worklet processor was not registered')
  const processor = new Processor()
  const totalSourceSamples = sourceRate
  for (let offset = 0; offset < totalSourceSamples; offset += 128) {
    const blockLength = Math.min(128, totalSourceSamples - offset)
    const input = new Float32Array(blockLength)
    input.fill(0.5)
    processor.process([[input]], [[new Float32Array(blockLength)]])
  }
  processor.port.onmessage({ data: { type: 'stop' } })

  const pcmMessages = processor.port.messages.filter((message) => message.type === 'pcm')
  const flushedMessages = processor.port.messages.filter((message) => message.type === 'flushed')
  assert.equal(flushedMessages.length, 1, `${sourceRate} Hz must acknowledge exactly one flush`)
  assert.ok(pcmMessages.length > 0, `${sourceRate} Hz produced no PCM`)
  for (const message of pcmMessages) {
    assert.ok(message.buffer instanceof ArrayBuffer)
    assert.ok(message.buffer.byteLength > 0 && message.buffer.byteLength <= 640)
    assert.equal(message.buffer.byteLength % 2, 0)
  }

  const pcmBytes = pcmMessages.reduce((total, message) => total + message.buffer.byteLength, 0)
  assert.equal(pcmBytes, 16000 * 2, `${sourceRate} Hz did not resample one second to 16 kHz PCM`)
  assert.equal(new DataView(pcmMessages[0].buffer).getInt16(0, true), 16384, 'PCM must be little-endian Int16')
}

for (const sourceRate of [16000, 44100, 48000]) runWorklet(sourceRate)

console.log('Phase 6 verification passed: 16/44.1/48 kHz -> 16 kHz PCM, 640-byte frames, LE Int16, no Worklet network access, 1 MiB ASR backpressure.')
