import { afterEach, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createAnalysis, getSource, getIssues, publishPlan, publicationUrl, sendAnswer, updateStep, waitForAnalysis } from '../src/api/task-api.ts'
import { taskForScreen, messageForScreen } from '../src/api/task-view.ts'

const originalFetch = globalThis.fetch
afterEach(() => { globalThis.fetch = originalFetch })
const signal = () => new AbortController().signal
const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
const recorded = JSON.parse(await readFile(new URL('../../examples/recorded/DEMO-1.json', import.meta.url)))
const analysis = {
  analysis_id: 'analysis-test', task_id: 'DEMO-1', status: recorded.status,
  execution_mode: 'replay', result_revision: 7, result: recorded,
  step_progress: [], publication: { status: 'not_published' }, error: null,
}

test('Go create receives Jira URL and caller-provided idempotency key', async () => {
  globalThis.fetch = async (url, options) => {
    assert.equal(url, '/api/v1/analyses')
    assert.equal(options.method, 'POST')
    assert.deepEqual(JSON.parse(options.body), { jira_url: 'https://jira.example/browse/DEMO-1' })
    assert.equal(options.headers['Idempotency-Key'], 'retry-same-operation')
    return json({ analysis_id: 'id', status: 'queued', status_url: '/api/v1/analyses/id' }, 202)
  }
  assert.equal((await createAnalysis('https://jira.example/browse/DEMO-1', 'retry-same-operation', signal())).analysis_id, 'id')
})

test('progress uses current revision and requested checkbox value', async () => {
  globalThis.fetch = async (url, options) => {
    assert.equal(url, '/api/v1/analyses/id/steps/v7-s2')
    assert.equal(options.method, 'PATCH')
    assert.deepEqual(JSON.parse(options.body), { result_revision: 7, completed: true })
    return json({ step_id: 'v7-s2', completed: true })
  }
  assert.equal((await updateStep('id', 'v7-s2', 7, true, signal())).completed, true)
})

test('source IDs are encoded as opaque keys and revision is pinned', async () => {
  globalThis.fetch = async (url) => {
    assert.equal(url, '/api/v1/analyses/id/sources/task%3ADEMO%2F1?result_revision=7')
    return json(recorded.sources[0])
  }
  await getSource('id', 'task:DEMO/1', 7, signal())
})

test('publication requires explicit confirmation and does not hide server failure', async () => {
  globalThis.fetch = async (_url, options) => {
    assert.deepEqual(JSON.parse(options.body), { result_revision: 7, confirmed: true })
    assert.equal(options.headers['Idempotency-Key'], 'publish-retry-key')
    return json({ error: { code: 'publish_failed', message: 'Не удалось сохранить план' } }, 500)
  }
  await assert.rejects(publishPlan('id', 7, 'publish-retry-key', signal()), /Не удалось сохранить план/)
})

test('replay clarification error is propagated, no local successful answer is fabricated', async () => {
  globalThis.fetch = async (_url, options) => {
    assert.deepEqual(JSON.parse(options.body), { answer: 'Email' })
    return json({ error: { code: 'replay_input_not_recorded', message: 'Replay unavailable' } }, 422)
  }
  await assert.rejects(sendAnswer('id', 'Email', 'answer-key', signal()), (error) => error.code === 'replay_input_not_recorded' && error.status === 422)
})

test('polling ends at a terminal result and cancellation stops queued polling', async () => {
  let requests = 0
  globalThis.fetch = async () => { requests++; return json(analysis) }
  const states = []
  assert.equal((await waitForAnalysis('id', signal(), (state) => states.push(state))).status, 'ready')
  assert.deepEqual(states, ['ready'])
  assert.equal(requests, 1)
  const controller = new AbortController()
  globalThis.fetch = async () => { requests++; return json({ ...analysis, status: 'queued', result: null }) }
  const pending = waitForAnalysis('id', controller.signal, () => controller.abort())
  await assert.rejects(pending)
  assert.equal(requests, 2)
})

test('recorded Go result maps real step IDs, source references and evidence without replacing their text', () => {
  const task = taskForScreen(analysis, [{ task_id: 'DEMO-1', title: 'Server title', jira_url: '' }])
  assert.equal(task.title, 'Server title')
  assert.equal(task.steps[0].id, 'v7-s1')
  assert.deepEqual(task.steps[0].sourceIds, recorded.plan.steps[0].source_ids)
  assert.equal(task.steps[0].description, recorded.plan.steps[0].expected_result)
  assert.equal(task.sources[0].content, recorded.sources[0].content)
  assert.deepEqual(messageForScreen(analysis).findings, recorded.plan.findings)
  assert.equal(taskForScreen({ ...analysis, result: null }, []), null)
})

test('publication links reject scripts and external origins', () => {
  assert.equal(publicationUrl('javascript:alert(1)'), '')
  assert.equal(publicationUrl('https://untrusted.example/plan'), '')
  assert.equal(publicationUrl('//untrusted.example/plan'), '')
  assert.equal(publicationUrl('/api/v1/mock-jira/plans/plan-123'), '/api/v1/mock-jira/plans/plan-123')
})

test('offline and malformed catalog responses are errors, never demo fallback', async () => {
  globalThis.fetch = async () => { throw new TypeError('network down') }
  await assert.rejects(getIssues(signal()), (error) => error.code === 'network_error')
  globalThis.fetch = async () => json({})
  await assert.rejects(getIssues(signal()), (error) => error.code === 'invalid_response')
})
