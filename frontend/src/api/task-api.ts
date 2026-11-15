import type { Analysis, AnalysisStatus, ApiSource, Issue, QueuedAnalysis } from '../types/api'

// По умолчанию /api перенаправляет Vite. В сборке нужен reverse proxy или адрес Go API.
const baseUrl = (import.meta.env?.VITE_API_BASE_URL || '').replace(/\/$/, '')

export class ApiError extends Error {
  code: string
  status: number
  constructor(message: string, code: string, status: number) {
    super(message)
    this.code = code
    this.status = status
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return 'Не удалось выполнить запрос. Попробуйте ещё раз.'
}

const errorMessages: Record<string, string> = {
  replay_input_not_recorded: 'На сервере нет записанного результата для этого запроса. Обработку новых уточнений нужно включить на стороне бэкенда.',
  corpus_mismatch: 'Материалы на сервере изменились. Запустите разбор задачи заново.',
  source_access_denied: 'Нет доступа к источникам этого разбора.',
  conflict: 'Данные изменились на сервере. Обновите результат и повторите действие.',
  not_ready: 'Бэкенд ещё не готов к работе. Попробуйте позже.',
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const timeout = AbortSignal.timeout(20000)
  const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout
  let response: Response
  try {
    response = await fetch(baseUrl + path, {
      ...options,
      signal,
      headers: { 'Content-Type': 'application/json', ...options.headers },
    })
  } catch (error) {
    if (options.signal?.aborted) throw error
    throw new ApiError('Не удалось связаться с бэкендом. Проверьте, что Go-сервер запущен и адрес API указан верно.', 'network_error', 0)
  }
  const body = await response.json().catch(() => null)
  if (!response.ok) {
    const code = body?.error?.code || 'server_error'
    const message = errorMessages[code] || body?.error?.message || 'Сервер недоступен или вернул ошибку. Проверьте запуск бэкенда.'
    throw new ApiError(message, code, response.status)
  }
  if (body === null) throw new ApiError('Сервер вернул пустой ответ.', 'invalid_response', response.status)
  return body as T
}

export function getProfile(signal: AbortSignal) {
  return request<{ profile: { id: string; display_name: string; allowed_scopes: string[] } }>('/api/v1/me', { signal })
}
export async function getIssues(signal: AbortSignal): Promise<Issue[]> {
  const body = await request<{ issues: Issue[] }>('/api/v1/demo/issues', { signal })
  if (!Array.isArray(body.issues)) throw new ApiError('Сервер не вернул список задач.', 'invalid_response', 200)
  return body.issues
}
export function createAnalysis(jiraUrl: string, key: string, signal: AbortSignal) {
  return request<QueuedAnalysis>('/api/v1/analyses', {
    method: 'POST', body: JSON.stringify({ jira_url: jiraUrl }), headers: { 'Idempotency-Key': key }, signal,
  })
}
export function getAnalysis(id: string, signal: AbortSignal) {
  return request<Analysis>(`/api/v1/analyses/${encodeURIComponent(id)}`, { signal })
}
export function sendAnswer(id: string, answer: string, key: string, signal: AbortSignal) {
  return request<QueuedAnalysis>(`/api/v1/analyses/${encodeURIComponent(id)}/answers`, {
    method: 'POST', body: JSON.stringify({ answer }), headers: { 'Idempotency-Key': key }, signal,
  })
}
export function updateStep(id: string, stepId: string, revision: number, completed: boolean, signal: AbortSignal) {
  return request<{ step_id: string; completed: boolean }>(`/api/v1/analyses/${encodeURIComponent(id)}/steps/${encodeURIComponent(stepId)}`, {
    method: 'PATCH', body: JSON.stringify({ result_revision: revision, completed }), signal,
  })
}
export function getSource(id: string, sourceId: string, revision: number, signal: AbortSignal) {
  return request<ApiSource>(`/api/v1/analyses/${encodeURIComponent(id)}/sources/${encodeURIComponent(sourceId)}?result_revision=${revision}`, { signal })
}
export function publishPlan(id: string, revision: number, key: string, signal: AbortSignal) {
  return request<{ plan_id: string; url: string }>(`/api/v1/analyses/${encodeURIComponent(id)}/publish`, {
    method: 'POST', body: JSON.stringify({ result_revision: revision, confirmed: true }), headers: { 'Idempotency-Key': key }, signal,
  })
}

// Ссылки от сервера разрешаем только для API сохранённых планов.
export function publicationUrl(path: string): string {
  if (!/^\/api\/v1\/mock-jira\/plans\/[A-Za-z0-9-]+$/.test(path)) return ''
  return baseUrl + path
}

function pollDelay(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return }
    const abort = () => { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 2000)
    signal.addEventListener('abort', abort, { once: true })
  })
}

export async function waitForAnalysis(id: string, signal: AbortSignal, onStatus: (status: AnalysisStatus) => void) {
  const started = Date.now()
  while (!signal.aborted) {
    const analysis = await getAnalysis(id, signal)
    onStatus(analysis.status)
    if (analysis.status !== 'queued' && analysis.status !== 'collecting_context') return analysis
    if (Date.now() - started > 15 * 60 * 1000) {
      throw new ApiError('Разбор ещё не завершён. Можно продолжить ожидание, не создавая новую задачу.', 'poll_timeout', 0)
    }
    await pollDelay(signal)
  }
  throw signal.reason
}
