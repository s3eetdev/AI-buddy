import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import App from '../src/App'
import { TaskWorkspace } from '../src/components/task-workspace'
import { UserProfile } from '../src/components/user-profile'
import type { Analysis } from '../src/types/api'

const source = { id: 'handler', kind: 'code', scope: 'demo', path: 'service/handler.go', content: 'publish(OrderCancelled)' }
const ready = (): Analysis => ({
  analysis_id: 'a1', task_id: 'DEMO-1', status: 'ready', execution_mode: 'replay', result_revision: 1,
  result: { task_id: 'DEMO-1', status: 'ready', sources: [source], plan: {
    summary: 'Проверить отмену заказа',
    findings: [
      { kind: 'fact', role: 'context', explanation: 'Событие публикуется', citations: [{ source_id: 'handler', start_line: 1, end_line: 1, quote: source.content }] },
      { kind: 'hypothesis', role: 'change_candidate', explanation: 'Возможна повторная доставка', citations: [] },
    ],
    steps: [{ action: 'Изучить обработчик', source_ids: ['handler'], expected_result: 'Понятен путь события' }],
    questions: [], limitations: ['Тестовый корпус'],
  } }, step_progress: [], publication: { status: 'not_published' }, error: null,
})
const clarify = () => {
  const a = ready()
  a.status = 'needs_clarification'
  a.result!.status = 'needs_clarification'
  a.result!.plan.questions = ['Какой канал?', 'Кто получатель?']
  return a
}
type Call = { path: string; method: string; body: Record<string, unknown>; key: string | null }
function server(result = ready(), overrides: (call: Call) => Response | Promise<Response> | undefined = () => undefined) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit = {}) => {
    const call = { path: String(url), method: options.method || 'GET', body: JSON.parse(String(options.body || '{}')), key: new Headers(options.headers).get('Idempotency-Key') }
    calls.push(call)
    const custom = await overrides(call)
    if (custom) return custom
    if (call.path.endsWith('/me')) return json({ profile: { id: 'intern', display_name: 'Иван Стажёр', allowed_scopes: ['demo'] } })
    if (call.path.endsWith('/demo/issues')) return json({ issues: [{ task_id: 'DEMO-1', title: 'Отмена заказа', jira_url: 'https://demo-jira.local/browse/DEMO-1' }] })
    if (call.method === 'POST' && call.path.endsWith('/analyses')) return json({ analysis_id: 'a1', status: 'queued' })
    if (call.path.includes('/sources/')) return json(source)
    if (call.method === 'PATCH') return json({ step_id: 'v1-s1', completed: call.body.completed })
    if (call.path.endsWith('/publish')) return json({ plan_id: 'p1', url: '/api/v1/mock-jira/plans/p1' })
    return json(result)
  }))
  return calls
}
const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
async function analyze() {
  await waitFor(() => expect(document.querySelectorAll('datalist option')).toHaveLength(1))
  fireEvent.change(screen.getByLabelText('Задача из Jira'), { target: { value: 'DEMO-1' } })
  fireEvent.click(screen.getByRole('button', { name: 'Разобрать задачу' }))
}
async function readyScreen() { await screen.findByRole('heading', { name: 'Отмена заказа' }) }

describe('Полный пользовательский путь с HTTP-имитацией', () => {
  it('показывает профиль, доказательства, материал, прогресс и подтверждённую публикацию', async () => {
    const calls = server()
    render(<App />)
    await screen.findByText('Иван Стажёр')
    await analyze(); await readyScreen()
    expect(screen.getByText('Событие публикуется')).toBeVisible()
    expect(screen.getByText('Гипотеза — требует проверки')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: /handler · строки/ }))
    expect(await screen.findByText(source.content, { selector: 'pre' })).toBeVisible()
    expect(calls.find(c => c.path.includes('/sources/'))?.path).toContain('result_revision=1')
    fireEvent.click(screen.getByRole('tab', { name: /Первые шаги/ }))
    fireEvent.click(screen.getByRole('checkbox'))
    await waitFor(() => expect(screen.getByRole('checkbox')).toBeChecked())
    expect(calls.find(c => c.method === 'PATCH')?.body).toEqual({ result_revision: 1, completed: true })
    fireEvent.click(screen.getByRole('checkbox'))
    await waitFor(() => expect(screen.getByRole('checkbox')).not.toBeChecked())
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить в Jira' }))
    const link = await screen.findByRole('link', { name: 'Открыть сохранённый план' })
    expect(link).toHaveAttribute('href', '/api/v1/mock-jira/plans/p1')
    expect(calls.find(c => c.path.endsWith('/publish'))?.body).toEqual({ result_revision: 1, confirmed: true })
    expect(screen.getByRole('button', { name: 'План сохранён' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'handler.go' }))
    expect(screen.getByRole('tab', { name: /Материалы/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('сохраняет черновик при ошибке уточнения и обрабатывает новую версию после повтора', async () => {
    const result = clarify()
    let attempts = 0
    const keys: (string | null)[] = []
    server(result, c => {
      if (c.path.endsWith('/answers')) {
        keys.push(c.key)
        if (++attempts === 1) return json({ error: { code: 'replay_input_not_recorded' } }, 422)
        Object.assign(result, ready(), { result_revision: 2 })
        return json({ analysis_id: 'a1', status: 'queued' }, 202)
      }
    })
    render(<TaskWorkspace />); await analyze(); await readyScreen()
    const input = screen.getByLabelText('Ответ на уточнение')
    expect(screen.getByRole('button', { name: 'Ответить и обновить план' })).toBeDisabled()
    fireEvent.change(input, { target: { value: 'Email покупателю' } })
    fireEvent.click(screen.getByRole('tab', { name: /Первые шаги/ }))
    expect(screen.getByRole('button', { name: 'Сохранить в Jira' })).toBeDisabled()
    fireEvent.click(screen.getByRole('tab', { name: 'AI-buddy' }))
    expect(input).toHaveValue('Email покупателю')
    fireEvent.click(screen.getByRole('button', { name: 'Ответить и обновить план' }))
    await screen.findByText(/На сервере нет записанного результата/)
    expect(input).toHaveValue('Email покупателю')
    fireEvent.click(screen.getByRole('button', { name: 'Ответить и обновить план' }))
    await screen.findByText('План подготовлен')
    expect(keys[0]).toBe(keys[1])
    expect(screen.getByText('Email покупателю')).toBeVisible()
    expect(screen.queryByLabelText('Ответ на уточнение')).not.toBeInTheDocument()
  })

  it('не изображает успешную публикацию при сбое, повтор использует тот же ключ', async () => {
    let attempts = 0
    const keys: (string | null)[] = []
    server(ready(), c => {
      if (c.path.endsWith('/publish')) {
        keys.push(c.key)
        if (++attempts === 1) return json({ error: { code: 'publish_failed', message: 'Ошибка записи плана' } }, 500)
      }
    })
    render(<TaskWorkspace />); await analyze(); await readyScreen()
    fireEvent.click(screen.getByRole('tab', { name: /Первые шаги/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить в Jira' }))
    await screen.findByText('Ошибка записи плана')
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить в Jira' }))
    await screen.findByRole('link', { name: 'Открыть сохранённый план' })
    expect(keys[0]).toBe(keys[1])
  })

  it('повторяет чтение источника после отказа без раскрытия старого материала', async () => {
    let reads = 0
    server(ready(), c => c.path.includes('/sources/') && ++reads === 1
      ? json({ error: { code: 'source_access_denied' } }, 403) : undefined)
    render(<TaskWorkspace />); await analyze(); await readyScreen()
    fireEvent.click(screen.getByRole('tab', { name: /Материалы/ }))
    fireEvent.click(screen.getByRole('button', { name: /handler.go/ }))
    await screen.findByText('Нет доступа к источникам этого разбора.')
    expect(screen.queryByText(source.content, { selector: 'pre' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Повторить загрузку' }))
    expect(await screen.findByText(source.content, { selector: 'pre' })).toBeVisible()
  })

  it('валидирует ввод, восстанавливает каталог и профиль после недоступности', async () => {
    let catalog = 0
    server(ready(), c => {
      if (c.path.endsWith('/me')) return json({}, 503)
      if (c.path.endsWith('/demo/issues') && ++catalog === 1) throw new TypeError('offline')
    })
    render(<App />)
    await screen.findByText('Профиль недоступен')
    await screen.findByRole('button', { name: 'Повторить подключение' })
    fireEvent.click(screen.getByRole('button', { name: 'Разобрать задачу' }))
    expect(screen.getByText('Введите ссылку Jira или выберите номер задачи из списка сервера.')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Повторить подключение' }))
    await analyze(); await readyScreen()
  })

  it('перечитывает созданный анализ после сбоя опроса и поддерживает клавиатуру вкладок', async () => {
    let reads = 0
    const calls = server(ready(), c => c.path === '/api/v1/analyses/a1' && ++reads === 1
      ? json({ error: { code: 'temporary', message: 'Временный сбой' } }, 503) : undefined)
    render(<TaskWorkspace />); await analyze()
    await screen.findByText('Временный сбой')
    fireEvent.click(screen.getByRole('button', { name: 'Обновить результат' }))
    await readyScreen()
    expect(calls.filter(c => c.method === 'POST' && c.path.endsWith('/analyses'))).toHaveLength(1)
    const buddy = screen.getByRole('tab', { name: 'AI-buddy' })
    fireEvent.keyDown(buddy, { key: 'ArrowLeft' })
    const materials = screen.getByRole('tab', { name: /Материалы/ })
    expect(materials).toHaveFocus()
    fireEvent.keyDown(materials, { key: 'ArrowRight' }); expect(buddy).toHaveFocus()
    fireEvent.keyDown(buddy, { key: 'End' }); expect(materials).toHaveFocus()
    fireEvent.keyDown(materials, { key: 'Home' }); expect(buddy).toHaveFocus()
    fireEvent.keyDown(buddy, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: /Первые шаги/ })).toHaveFocus()
    fireEvent.keyDown(buddy, { key: 'Tab' })
  })

  it.each(['failed', 'missing', 'insufficient'] as const)('обрабатывает результат %s без ложного успеха', async kind => {
    const result = ready()
    if (kind === 'failed') { result.status = 'failed'; result.error = { code: 'ai_invalid_response', message: 'Невалидный ответ модели' }; result.result = null }
    if (kind === 'missing') result.result = null
    if (kind === 'insufficient') {
      result.status = 'insufficient_context'; result.result!.status = 'insufficient_context'
      result.result!.sources = []; result.result!.plan.steps = []; result.result!.plan.findings = []
    }
    server(result); render(<TaskWorkspace />); await analyze()
    if (kind === 'failed') await screen.findByText('Невалидный ответ модели')
    else if (kind === 'missing') await screen.findByText('Сервер не вернул план. Попробуйте обновить результат.')
    else {
      await readyScreen()
      fireEvent.click(screen.getByRole('tab', { name: /Первые шаги/ }))
      expect(screen.getByRole('button', { name: 'Сохранить в Jira' })).toBeDisabled()
      fireEvent.click(screen.getByRole('tab', { name: /Материалы/ }))
      expect(within(screen.getByRole('tabpanel')).getAllByText('Доступных материалов нет.')).toHaveLength(2)
    }
  })

  it('отменяет запросы при размонтировании', async () => {
    const signals: AbortSignal[] = []
    vi.stubGlobal('fetch', vi.fn((_url, options) => { signals.push(options.signal); return new Promise(() => {}) }))
    const view = render(<App />)
    view.unmount()
    expect(signals).toHaveLength(2)
    expect(signals.every(s => s.aborted)).toBe(true)
  })

  it('показывает команду только если она передана сервером', () => {
    render(<UserProfile displayName="Иван Иванов" team="Доставка" />)
    expect(screen.getByText('ИИ')).toBeInTheDocument()
    expect(screen.getByText('Доставка')).toBeInTheDocument()
  })
})
