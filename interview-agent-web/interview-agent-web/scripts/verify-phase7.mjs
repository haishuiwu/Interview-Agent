import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const speechTypes = await readFile(new URL('../src/types/speech.ts', import.meta.url), 'utf8')
const asrClient = await readFile(new URL('../src/api/speech.ts', import.meta.url), 'utf8')
const speechHook = await readFile(new URL('../src/hooks/useInterviewSpeech.ts', import.meta.url), 'utf8')
const chatWindow = await readFile(new URL('../src/components/ChatWindow.tsx', import.meta.url), 'utf8')

for (const source of [asrClient, speechHook, chatWindow]) {
  assert.doesNotMatch(source, /DASHSCOPE_API_KEY|dashscope\.aliyuncs\.com/, 'Browser code must not contain provider credentials or endpoints')
}
assert.match(speechTypes, /type: 'asr\.warning'/, 'Speech protocol must define asr.warning')
assert.match(speechTypes, /code: 'REALTIME_DEGRADED'/, 'Speech protocol must constrain the degradation code')
assert.match(asrClient, /onWarning:/, 'ASR client must expose a non-terminal warning callback')
assert.match(asrClient, /this\.callbacks\.onWarning\(message\)/, 'ASR warning must reach the speech hook')
assert.match(speechHook, /asrDegradedRef\.current = true/, 'Speech hook must retain degradation across recording states')
assert.match(speechHook, /message\.degraded \|\| asrDegradedRef\.current/, 'Final draft must preserve server or observed degradation')
assert.match(speechHook, /partial: partial \|\| undefined/, 'A total ASR failure must preserve the latest partial text')
assert.match(chatWindow, /实时字幕暂时不可用，录音结束后将继续识别/, 'Recording UI must show the degradation notice')
assert.match(chatWindow, /已通过降级识别生成草稿/, 'Draft UI must label fallback recognition')
assert.match(chatWindow, /识别失败前的最后实时文本（仅供参考）/, 'Failure UI must retain the latest partial text')
assert.match(chatWindow, /answerInputRef/, 'Keyboard input fallback must remain available')

console.log('Phase 7 verification passed: warning protocol, degraded state/UI, provider isolation, and keyboard fallback are present.')
