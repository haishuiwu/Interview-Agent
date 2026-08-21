const TARGET_SAMPLE_RATE = 16000
const FRAME_SAMPLES = 320

class TrainingPCMCaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super()
    this.sourceToTargetRatio = sampleRate / TARGET_SAMPLE_RATE
    this.sourceIndex = 0
    this.nextOutputIndex = 0
    this.previousSample = 0
    this.hasPreviousSample = false
    this.frameBuffer = new ArrayBuffer(FRAME_SAMPLES * 2)
    this.frameView = new DataView(this.frameBuffer)
    this.frameSamples = 0
    this.active = true

    this.port.onmessage = (event) => {
      if (event.data?.type !== 'stop') return
      this.active = false
      this.flushFrame()
      this.port.postMessage({ type: 'flushed' })
    }
  }

  process(inputs, outputs) {
    // This node is connected through a zero-gain output solely to keep the
    // worklet in the active audio graph. Never copy microphone data to output.
    for (const output of outputs) {
      for (const channel of output) channel.fill(0)
    }
    if (!this.active) return true

    const channels = inputs[0]
    const frameLength = channels?.[0]?.length || 0
    for (let frame = 0; frame < frameLength; frame += 1) {
      let mono = 0
      for (const channel of channels) mono += channel[frame] || 0
      this.consumeSourceSample(mono / channels.length)
    }
    return true
  }

  consumeSourceSample(sample) {
    const index = this.sourceIndex
    if (!this.hasPreviousSample) {
      this.previousSample = sample
      this.hasPreviousSample = true
    }

    while (this.nextOutputIndex <= index) {
      const fraction = index === 0
        ? 1
        : Math.min(1, Math.max(0, this.nextOutputIndex - (index - 1)))
      const interpolated = this.previousSample + (sample - this.previousSample) * fraction
      this.writeSample(interpolated)
      this.nextOutputIndex += this.sourceToTargetRatio
    }

    this.previousSample = sample
    this.sourceIndex += 1
  }

  writeSample(sample) {
    const clipped = Math.max(-1, Math.min(1, sample))
    const pcm = clipped < 0
      ? Math.round(clipped * 0x8000)
      : Math.round(clipped * 0x7fff)
    this.frameView.setInt16(this.frameSamples * 2, pcm, true)
    this.frameSamples += 1
    if (this.frameSamples === FRAME_SAMPLES) this.publishFrame(this.frameBuffer)
  }

  flushFrame() {
    if (this.frameSamples > 0) {
      this.publishFrame(this.frameBuffer.slice(0, this.frameSamples * 2))
    }
  }

  publishFrame(buffer) {
    this.port.postMessage({ type: 'pcm', buffer }, [buffer])
    this.frameBuffer = new ArrayBuffer(FRAME_SAMPLES * 2)
    this.frameView = new DataView(this.frameBuffer)
    this.frameSamples = 0
  }
}

registerProcessor('training-pcm-capture', TrainingPCMCaptureProcessor)
