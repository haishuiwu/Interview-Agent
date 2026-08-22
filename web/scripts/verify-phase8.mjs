import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const envExample = await readFile(new URL('../../backend/.env.example', import.meta.url), 'utf8')
const backendReadme = await readFile(new URL('../../backend/README.md', import.meta.url), 'utf8')
const frontendReadme = await readFile(new URL('../README.md', import.meta.url), 'utf8')
const chatWindow = await readFile(new URL('../src/components/ChatWindow.tsx', import.meta.url), 'utf8')
const speechHook = await readFile(new URL('../src/hooks/useTrainingSpeech.ts', import.meta.url), 'utf8')

assert.match(envExample, /^SPEECH_ENABLED=false$/m, 'Speech must remain globally disabled by default')
assert.match(envExample, /^WEB_ALLOWED_ORIGINS=http:\/\/localhost:5173$/m, 'The development Origin must be explicit')
assert.doesNotMatch(envExample, /^WEB_ALLOWED_ORIGINS=\*$/m, 'Speech Origin must not use a wildcard')
assert.match(backendReadme, /第一批灰度：只开启 Coach 朗读/, 'Backend README must document TTS-first rollout')
assert.match(backendReadme, /SPEECH_ENABLED=false/, 'Backend README must document the global rollback flag')
assert.match(backendReadme, /user_id.*HMAC|HMAC.*user_id/s, 'Backend README must document privacy-safe speech logs')
assert.match(frontendReadme, /ASR final 不会自动触发评分/, 'Frontend README must preserve manual answer submission')
assert.match(chatWindow, /<textarea/, 'Keyboard answer input must remain in ChatWindow')
assert.match(chatWindow, /改用键盘/, 'Speech failures must retain a keyboard fallback action')
assert.match(speechHook, /onDraftReadyRef\.current\?\./, 'ASR final must be handed back as a draft callback')
assert.doesNotMatch(speechHook, /sendMessage\([^)]*answer/, 'Speech hook must not submit answers directly')

console.log('Phase 8 verification passed: safe feature default, exact Origin, TTS-first rollout, privacy logging docs, manual final submission, and keyboard fallback are present.')
