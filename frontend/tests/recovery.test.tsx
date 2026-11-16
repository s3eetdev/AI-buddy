import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TaskWorkspace } from '../src/components/task-workspace'
import type { Analysis } from '../src/types/api'

const result = (): Analysis => ({
  analysis_id: 'saved-id', task_id: 'DEMO-1', status: 'ready', execution_mode: 'replay', result_revision: 1,
  result: { task_id: 'DEMO-1', status: 'ready', sources: [], plan: {
    summary: 'План', findings: [], steps: [{ action: 'Первый шаг', source_ids: [], expected_result: 'Готово' }], questions: [], limitations: [],
  } },
  step_progress: [{ step_id: 'v1-s1', completed: true }],
  publication: { status: 'published', plan_id: 'p1', url: '/api/v1/mock-jira/plans/p1' }, error: null,
})
const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
function openSaved(handler: () => Response | Promise<Response>) {
  window.history.replaceState(null, '', '/?analysis_id=saved-id')
  const calls: { url: string; method: string }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit = {}) => {
    calls.push({ url: String(url), method: options.method || 'GET' })
    if (String(url).endsWith('/demo/issues')) return json({ issues: [{ task_id: 'DEMO-1', title: 'Задача', jira_url: 'https://demo-jira.local/browse/DEMO-1' }] })
    return handler()
  }))
  render(<TaskWorkspace />)
  return calls
}

describe('Восстановление анализа из URL', () => {
  it('восстанавливает план, прогресс и публикацию без создания анализа и позволяет начать новую задачу', async () => {
    const calls = openSaved(() => json(result()))
    await screen.findByRole('heading', { name: 'Задача' })
    expect(screen.getByText('Записанный пример')).toBeVisible()
    expect(screen.getByText('Используется ранее записанный ответ.')).toBeVisible()
    fireEvent.click(screen.getByRole('tab', { name: /Первые шаги/ }))
    expect(screen.getByRole('checkbox')).toBeChecked()
    expect(screen.getByRole('link', { name: 'Открыть сохранённый план' })).toHaveAttribute('href', '/api/v1/mock-jira/plans/p1')
    expect(calls.filter(c => c.method === 'POST')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Новая задача' }))
    expect(window.location.search).not.toContain('analysis_id')
    expect(screen.getByLabelText('Задача из Jira')).toHaveValue('')
    expect(screen.getByRole('heading', { name: 'Начнём с твоей задачи' })).toBeVisible()
  })

  it.each(['queued', 'collecting_context'] as const)('продолжает опрос анализа %s и показывает live-режим', async status => {
    let reads = 0
    const live = result(); live.execution_mode = 'live'
    const calls = openSaved(() => ++reads === 1 ? json({ ...live, status, result: null, result_revision: null }) : json(live))
    await screen.findByText(status === 'queued' ? 'Задача ожидает обработки…' : 'Собираю контекст задачи…')
    await screen.findByText('Live-анализ', {}, { timeout: 4000 })
    expect(screen.queryByText('Используется ранее записанный ответ.')).not.toBeInTheDocument()
    expect(calls.filter(c => c.url.endsWith('/analyses/saved-id'))).toHaveLength(2)
    expect(calls.filter(c => c.method === 'POST')).toHaveLength(0)
  })

  it.each([
    [404, 'Анализ не найден'], [401, 'Нет доступа к этому анализу'], [403, 'Нет доступа к этому анализу'], [0, 'Не удалось связаться с бэкендом'],
  ])('показывает ошибку восстановления %s и повторяет только чтение', async (status, message) => {
    let reads = 0
    const calls = openSaved(() => {
      if (++reads > 1) return json(result())
      if (status === 0) throw new TypeError('offline')
      return json({ error: { code: 'error' } }, status)
    })
    expect(await screen.findByText(new RegExp(message))).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Обновить результат' }))
    await screen.findByRole('heading', { name: 'Задача' })
    await waitFor(() => expect(calls.filter(c => c.method === 'POST')).toHaveLength(0))
  })
})
