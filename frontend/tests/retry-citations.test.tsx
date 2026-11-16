import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TaskWorkspace } from '../src/components/task-workspace'
import type { Analysis } from '../src/types/api'

const oldSource = { id: 'old', kind: 'code', scope: 'demo', path: 'old.go', content: 'старый снимок' }
const newSource = { id: 'new', kind: 'code', scope: 'demo', path: 'new.go', content: 'новый снимок' }
function result(id = 'a1', revision = 1): Analysis {
  const source = revision === 1 ? oldSource : newSource
  return { analysis_id: id, task_id: 'DEMO-1', status: revision === 1 ? 'needs_clarification' : 'ready', execution_mode: 'replay', result_revision: revision,
    result: { task_id: 'DEMO-1', status: revision === 1 ? 'needs_clarification' : 'ready', sources: [source], plan: {
      summary: `План версии ${revision}`, findings: [{ kind: 'fact', role: 'context', explanation: `Факт версии ${revision}`, citations: [{ source_id: source.id, start_line: 1, end_line: 1, quote: source.content }] }],
      steps: [], questions: revision === 1 ? ['Уточните'] : [], limitations: [],
    } }, step_progress: [], publication: { status: 'not_published' }, error: null }
}
const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
type Call = { path: string; method: string; key: string | null }
function mockServer(handler: (call: Call) => Response | Promise<Response> | undefined) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit = {}) => {
    const call = { path: String(url), method: options.method || 'GET', key: new Headers(options.headers).get('Idempotency-Key') }
    calls.push(call)
    const custom = await handler(call)
    if (custom) return custom
    if (call.path.endsWith('/demo/issues')) return json({ issues: [{ task_id: 'DEMO-1', title: 'Задача', jira_url: 'https://jira.example/browse/DEMO-1' }] })
    return json(result())
  }))
  return calls
}
async function submit() {
  await waitFor(() => expect(document.querySelectorAll('datalist option')).toHaveLength(1))
  fireEvent.change(screen.getByLabelText('Задача из Jira'), { target: { value: 'DEMO-1' } })
  fireEvent.click(screen.getByRole('button', { name: 'Разобрать задачу' }))
}
describe('Повторы и версии источников', () => {
  it('после failed явно создаёт новый анализ с другим ключом, включая failed после сбоя чтения', async () => {
    let post = 0
    let reads = 0
    const calls = mockServer(c => {
      if (c.method === 'POST' && c.path.endsWith('/analyses')) return json({ analysis_id: ++post === 1 ? 'a1' : 'a2', status: 'queued' })
      if (c.path.endsWith('/analyses/a1')) {
        if (++reads === 1) throw new TypeError('network')
        return json({ ...result(), status: 'failed', result: null, result_revision: null, error: { code: 'failed', message: 'Ошибка анализа' } })
      }
      if (c.path.endsWith('/analyses/a2')) return json(result('a2', 2))
    })
    render(<TaskWorkspace />); await submit()
    await screen.findByText(/Не удалось связаться/)
    fireEvent.click(screen.getByRole('button', { name: 'Обновить результат' }))
    await screen.findByText('Ошибка анализа')
    const retry = screen.getByRole('button', { name: 'Запустить новый разбор' })
    fireEvent.click(retry); fireEvent.click(retry)
    await screen.findByText('План версии 2')
    const posts = calls.filter(c => c.method === 'POST' && c.path.endsWith('/analyses'))
    expect(posts).toHaveLength(2); expect(posts[0].key).toBeTruthy(); expect(posts[0].key).not.toBe(posts[1].key)
    expect(window.location.search).toContain('analysis_id=a2')
  })
  it('повторяет неопределённый сбой POST с прежним ключом', async () => {
    let post = 0
    const calls = mockServer(c => {
      if (c.method === 'POST' && c.path.endsWith('/analyses')) {
        if (++post === 1) throw new TypeError('network')
        return json({ analysis_id: 'a1', status: 'queued' })
      }
    })
    render(<TaskWorkspace />); await submit()
    await screen.findByText(/Не удалось связаться/)
    fireEvent.click(screen.getByRole('button', { name: 'Разобрать задачу' }))
    await screen.findByText('План версии 1')
    const posts = calls.filter(c => c.method === 'POST' && c.path.endsWith('/analyses'))
    expect(posts).toHaveLength(2); expect(posts[0].key).toBeTruthy(); expect(posts[0].key).toBe(posts[1].key)
  })
  it('открывает цитату версии 1, отсутствующую в материалах версии 2, через snapshot версии 1', async () => {
    let revision = 1
    const calls = mockServer(c => {
      if (c.method === 'POST' && c.path.endsWith('/analyses')) return json({ analysis_id: 'a1', status: 'queued' })
      if (c.path.endsWith('/answers')) { revision = 2; return json({ analysis_id: 'a1', status: 'queued' }) }
      if (c.path.includes('/sources/old')) return json(oldSource)
      if (c.path.endsWith('/analyses/a1')) return json(result('a1', revision))
    })
    render(<TaskWorkspace />); await submit(); await screen.findByText('План версии 1')
    fireEvent.change(screen.getByLabelText('Ответ на уточнение'), { target: { value: 'Ответ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ответить и обновить план' }))
    await screen.findByText('План версии 2')
    fireEvent.click(screen.getByRole('button', { name: /old · строки/ }))
    expect(await screen.findByText('старый снимок', { selector: 'pre' })).toBeVisible()
    expect(calls.find(c => c.path.includes('/sources/old'))?.path).toContain('result_revision=1')
    expect(screen.queryByRole('button', { name: /old.go/ })).not.toBeInTheDocument()
  })
})
