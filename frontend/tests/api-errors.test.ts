import { expect, it, vi } from 'vitest'
import { errorMessage, getIssues, publicationUrl, waitForAnalysis } from '../src/api/task-api'
import { messageForScreen, sourceForScreen, taskForScreen } from '../src/api/task-view'
import type { Analysis } from '../src/types/api'

const signal = () => new AbortController().signal
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
it('обрабатывает пустой JSON, неизвестные ошибки и некорректный каталог без подстановки данных', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not-json')))
  await expect(getIssues(signal())).rejects.toMatchObject({ code: 'invalid_response' })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({}, 500)))
  await expect(getIssues(signal())).rejects.toMatchObject({ code: 'server_error', status: 500 })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({})))
  await expect(getIssues(signal())).rejects.toMatchObject({ code: 'invalid_response' })
  expect(errorMessage('unknown')).toContain('Не удалось')
})
it('опрашивает queued и collecting_context до конечного результата', async () => {
  vi.useFakeTimers()
  const fetcher = vi.fn().mockResolvedValueOnce(json({ status: 'queued' }))
    .mockResolvedValueOnce(json({ status: 'collecting_context' }))
    .mockResolvedValueOnce(json({ status: 'ready' }))
  vi.stubGlobal('fetch', fetcher)
  const onStatus = vi.fn()
  const result = waitForAnalysis('a1', signal(), onStatus)
  await vi.advanceTimersByTimeAsync(4000)
  await expect(result).resolves.toMatchObject({ status: 'ready' })
  expect(onStatus.mock.calls.flat()).toEqual(['queued', 'collecting_context', 'ready'])
})
it('останавливает опрос по отмене, в том числе до первого запроса', async () => {
  vi.useFakeTimers()
  const controller = new AbortController()
  vi.stubGlobal('fetch', vi.fn(async () => json({ status: 'queued' })))
  const result = waitForAnalysis('a1', controller.signal, () => {})
  const rejected = expect(result).rejects.toBe('closed')
  await vi.advanceTimersByTimeAsync(1)
  controller.abort('closed')
  await rejected
  await expect(waitForAnalysis('a1', controller.signal, () => {})).rejects.toBe('closed')
})
it('ограничивает длительность опроса и сохраняет возможность перечитать результат', async () => {
  vi.useFakeTimers()
  vi.stubGlobal('fetch', vi.fn(async () => json({ status: 'queued' })))
  const result = waitForAnalysis('a1', signal(), () => {})
  const rejected = expect(result).rejects.toMatchObject({ code: 'poll_timeout' })
  await vi.advanceTimersByTimeAsync(15 * 60 * 1000 + 2000)
  await rejected
})
it('не маскирует отмену сетевого запроса как отказ сервера', async () => {
  const controller = new AbortController(); controller.abort('closed')
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('closed', 'AbortError')))
  await expect(getIssues(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
})
it('не создаёт поддельный результат и ограничивает ссылки публикации', () => {
  const empty = { result: null, result_revision: null } as Analysis
  expect(taskForScreen(empty, [])).toBeNull()
  expect(() => messageForScreen(empty)).toThrow('Сервер не вернул результат')
  expect(sourceForScreen({ id: 'id', path: '', content: 'text', kind: 'documentation', scope: 'demo' }).title).toBe('id')
  for (const path of ['javascript:alert(1)', '//evil.test', 'https://evil.test/plan', '/api/v1/mock-jira/plans/id?x=1']) expect(publicationUrl(path)).toBe('')
})
