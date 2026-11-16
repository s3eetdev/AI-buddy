import { useEffect, useRef, useState } from 'react'
import type { ChangeEvent, FormEvent, KeyboardEvent } from 'react'
import type { Analysis, AnalysisStatus, Issue } from '../types/api'
import type { ChatMessage, WorkspaceTab } from '../types/task'
import { ApiError, createAnalysis, errorMessage, getIssues, publicationUrl, publishPlan, sendAnswer, updateStep, waitForAnalysis } from '../api/task-api'
import { messageForScreen, taskForScreen } from '../api/task-view'
import { BuddyChat } from './buddy-chat'
import { TaskSteps } from './task-steps'
import { TaskMaterials } from './task-materials'
import './task-workspace.css'

const tabs: { id: WorkspaceTab; title: string }[] = [
  { id: 'buddy', title: 'AI-buddy' },
  { id: 'steps', title: 'Первые шаги' },
  { id: 'materials', title: 'Материалы' },
]
type Operation = 'analyze' | 'answer' | 'step' | 'publish'
type SaveStatus = 'idle' | 'saving' | 'saved' | 'error'

function setAnalysisUrl(id: string | null) {
  const url = new URL(window.location.href)
  if (id) url.searchParams.set('analysis_id', id)
  else url.searchParams.delete('analysis_id')
  window.history.replaceState(window.history.state, '', url)
}

function recoveryError(error: unknown): unknown {
  if (error instanceof ApiError && error.status === 404) return new Error('Анализ не найден. Проверьте ссылку или начните новую задачу.')
  if (error instanceof ApiError && (error.status === 401 || error.status === 403)) return new Error('Нет доступа к этому анализу.')
  return error
}

export function TaskWorkspace() {
  const [initialAnalysisId] = useState(() => new URL(window.location.href).searchParams.get('analysis_id'))
  const [taskInput, setTaskInput] = useState('')
  const [inputError, setInputError] = useState('')
  const [issues, setIssues] = useState<Issue[]>([])
  const [catalogError, setCatalogError] = useState('')
  const [catalogAttempt, setCatalogAttempt] = useState(0)
  const [analysis, setAnalysis] = useState<Analysis | null>(null)
  const [analysisId, setAnalysisId] = useState<string | null>(initialAnalysisId)
  const [activeTab, setActiveTab] = useState<WorkspaceTab>('buddy')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle')
  const [operation, setOperation] = useState<Operation | null>(initialAnalysisId ? 'analyze' : null)
  const [loadingStatus, setLoadingStatus] = useState<AnalysisStatus>('queued')
  const [selectedSource, setSelectedSource] = useState<{ id: string; analysisId: string; revision: number } | null>(null)
  const [terminalFailed, setTerminalFailed] = useState(false)
  const createKeyName = useRef<string | null>(null)
  const requestController = useRef<AbortController | null>(null)
  // Повтор запроса с тем же ключом не создаёт вторую операцию на сервере.
  const requestKeys = useRef<Record<string, string>>({})
  const tabButtons = useRef<(HTMLButtonElement | null)[]>([])

  const task = analysis ? taskForScreen(analysis, issues) : null
  const completedStepIds = (analysis?.step_progress || [])
    .filter((step) => step.completed)
    .map((step) => step.step_id)
  const isAnalyzing = operation === 'analyze'
  const isUpdatingPlan = operation === 'answer'

  useEffect(() => {
    const controller = new AbortController()
    getIssues(controller.signal)
      .then(setIssues)
      .catch((error) => {
        if (!controller.signal.aborted) setCatalogError(errorMessage(error))
      })
    return () => controller.abort()
  }, [catalogAttempt])

  useEffect(() => {
    return () => requestController.current?.abort()
  }, [])

  function operationKey(name: string) {
    if (!requestKeys.current[name]) requestKeys.current[name] = crypto.randomUUID()
    return requestKeys.current[name]
  }

  // Общая обработка ожидания, ошибок и отмены при закрытии страницы.
  async function runRequest(kind: Operation, action: (signal: AbortSignal) => Promise<void>) {
    if (requestController.current) return false
    const controller = new AbortController()
    requestController.current = controller
    setOperation(kind)
    setInputError('')
    try {
      await action(controller.signal)
      return true
    } catch (error) {
      if (!controller.signal.aborted) {
        setInputError(errorMessage(error))
        if (kind === 'publish') setSaveStatus('error')
      }
      return false
    } finally {
      if (requestController.current === controller) requestController.current = null
      if (!controller.signal.aborted) setOperation(null)
    }
  }

  function showResult(result: Analysis, keepHistory: boolean) {
    if (result.status === 'failed') {
      setTerminalFailed(true)
      if (createKeyName.current) delete requestKeys.current[createKeyName.current]
      throw new ApiError(result.error?.message || 'Не удалось разобрать задачу.', result.error?.code || 'analysis_failed', 500)
    }
    if (!result.result || result.result_revision === null) {
      throw new Error('Сервер не вернул план. Попробуйте обновить результат.')
    }
    const message = messageForScreen(result)
    setTerminalFailed(false)
    setAnalysis(result)
    setSaveStatus(result.publication.status === 'published' ? 'saved' : 'idle')
    setSelectedSource(null)
    setMessages((previous) => {
      if (!keepHistory) return [message]
      return [...previous.filter((item) => item.id !== message.id), message]
    })
  }

  // URL содержит только идентификатор; восстановление читает существующий анализ.
  useEffect(() => {
    const id = initialAnalysisId
    if (!id) return
    const controller = new AbortController()
    requestController.current = controller
    void waitForAnalysis(id, controller.signal, setLoadingStatus)
      .then((result) => {
        if (controller.signal.aborted) return
        setTaskInput(result.task_id)
        showResult(result, false)
      })
      .catch((error) => {
        if (!controller.signal.aborted) setInputError(errorMessage(recoveryError(error)))
      })
      .finally(() => {
        if (requestController.current === controller) requestController.current = null
        if (!controller.signal.aborted) setOperation(null)
      })
    return () => {
      controller.abort()
      if (requestController.current === controller) requestController.current = null
    }
  }, [initialAnalysisId])

  function handleTaskInputChange(event: ChangeEvent<HTMLInputElement>) {
    setTaskInput(event.target.value)
    setInputError('')
  }

  function startNewTask() {
    requestController.current?.abort()
    requestController.current = null
    setOperation(null)
    setAnalysisUrl(null)
    setAnalysisId(null)
    setAnalysis(null)
    setTerminalFailed(false)
    setTaskInput('')
    setInputError('')
    setMessages([])
    setSaveStatus('idle')
    setSelectedSource(null)
    setActiveTab('buddy')
  }

  function handleTaskSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void analyzeTask()
  }

  // 1. Отправляем ссылку в Go и опрашиваем статус, пока сервер обрабатывает задачу.
  async function analyzeTask() {
    const input = taskInput.trim()
    const issue = issues.find((item) => item.task_id.toUpperCase() === input.toUpperCase())
    let jiraUrl = issue?.jira_url || input
    try {
      const url = new URL(jiraUrl)
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error()
      jiraUrl = url.href
    } catch {
      setInputError('Введите ссылку Jira или выберите номер задачи из списка сервера.')
      return
    }

    await runRequest('analyze', async (signal) => {
      setAnalysis(null)
      setAnalysisId(null)
      setAnalysisUrl(null)
      setTerminalFailed(false)
      setMessages([])
      setSaveStatus('idle')
      setSelectedSource(null)
      setActiveTab('buddy')
      setLoadingStatus('queued')
      const keyName = 'create:' + jiraUrl
      createKeyName.current = keyName
      const queued = await createAnalysis(jiraUrl, operationKey(keyName), signal)
      setAnalysisId(queued.analysis_id)
      setAnalysisUrl(queued.analysis_id)
      const result = await waitForAnalysis(queued.analysis_id, signal, setLoadingStatus)
      showResult(result, false)
      delete requestKeys.current[keyName]
    })
  }

  // Продолжаем уже созданный разбор после обрыва связи, не создавая его заново.
  async function refreshResult() {
    if (!analysisId) return
    await runRequest('analyze', async (signal) => {
      try {
        const result = await waitForAnalysis(analysisId, signal, setLoadingStatus)
        setTaskInput(result.task_id)
        showResult(result, true)
      } catch (error) {
        throw recoveryError(error)
      }
    })
  }

  // 2. Ответ передаётся серверу; новый план появляется только из его ответа.
  async function answerClarification(answer: string) {
    if (!analysis || analysis.result_revision === null || !task || task.status !== 'needs_clarification') return false
    const text = answer.trim().slice(0, 2000)
    if (!text) return false
    return runRequest('answer', async (signal) => {
      await sendAnswer(analysis.analysis_id, text, operationKey(`answer:${analysis.analysis_id}:${analysis.result_revision}:${text}`), signal)
      setMessages((previous) => [...previous, { id: crypto.randomUUID(), role: 'user', analysisId: analysis.analysis_id, revision: analysis.result_revision!, text }])
      const result = await waitForAnalysis(analysis.analysis_id, signal, setLoadingStatus)
      showResult(result, true)
    })
  }

  // 3. Галочка меняется после подтверждения сохранения бэкендом.
  async function toggleStep(stepId: string) {
    if (!analysis || analysis.result_revision === null) return
    const revision = analysis.result_revision
    await runRequest('step', async (signal) => {
      const progress = await updateStep(analysis.analysis_id, stepId, revision, !completedStepIds.includes(stepId), signal)
      setAnalysis({
        ...analysis,
        step_progress: [...analysis.step_progress.filter((step) => step.step_id !== stepId), progress],
      })
    })
  }

  // 4. Нажатие кнопки — явное подтверждение записи плана. Успех ждём от API.
  async function savePlan() {
    if (!analysis || analysis.status !== 'ready' || analysis.result_revision === null) return
    const revision = analysis.result_revision
    await runRequest('publish', async (signal) => {
      setSaveStatus('saving')
      const receipt = await publishPlan(analysis.analysis_id, revision, operationKey(`publish:${analysis.analysis_id}:${analysis.result_revision}`), signal)
      if (!receipt.plan_id || !publicationUrl(receipt.url)) throw new Error('Сервер не вернул ссылку на сохранённый план.')
      setAnalysis({ ...analysis, publication: { status: 'published', ...receipt } })
      setSaveStatus('saved')
    })
  }

  function openSource(sourceId: string, analysisId = analysis?.analysis_id, revision = analysis?.result_revision) {
    if (!analysisId || revision == null) return
    setSelectedSource({ id: sourceId, analysisId, revision })
    setActiveTab('materials')
    tabButtons.current[2]?.focus()
  }

  // Tab переводит фокус к вкладкам; стрелки, Home и End переключают их.
  function handleTabKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    let nextIndex = index

    if (event.key === 'ArrowRight') {
      nextIndex = index + 1
      if (nextIndex === tabs.length) nextIndex = 0
    } else if (event.key === 'ArrowLeft') {
      nextIndex = index - 1
      if (nextIndex < 0) nextIndex = tabs.length - 1
    } else if (event.key === 'Home') {
      nextIndex = 0
    } else if (event.key === 'End') {
      nextIndex = tabs.length - 1
    } else {
      return
    }

    event.preventDefault()
    setActiveTab(tabs[nextIndex].id)
    tabButtons.current[nextIndex]?.focus()
  }

  // Заранее определяем подпись и оформление статуса, чтобы не усложнять JSX.
  let statusText = 'Недостаточно данных'
  if (isUpdatingPlan) {
    statusText = 'Повторный разбор…'
  } else if (task?.status === 'needs_clarification') {
    statusText = 'Требуется уточнение'
  } else if (task?.status === 'ready') {
    statusText = 'План подготовлен'
  }

  let statusClass = 'task-card__status'
  if (isUpdatingPlan || task?.status !== 'ready') {
    statusClass += ' task-card__status--pending'
  }

  const isBusy = operation !== null

  return (
    <section className="task-workspace" aria-label="Рабочая область задачи">
      <form className="task-entry" onSubmit={handleTaskSubmit}>
        <label className="task-entry__label" htmlFor="jira-task">
          Задача из Jira
        </label>
        <input
          className="task-entry__input"
          id="jira-task"
          value={taskInput}
          onChange={handleTaskInputChange}
          placeholder="Ссылка или номер задачи"
          autoComplete="off"
          list="server-issues"
          disabled={isBusy || Boolean(analysisId && !terminalFailed)}
          aria-invalid={Boolean(inputError)}
          aria-describedby={inputError ? 'task-input-error' : undefined}
        />
        <button className="task-entry__button" type="submit" disabled={isBusy || Boolean(analysisId && !terminalFailed)}>
          {isAnalyzing ? 'Разбираю…' : terminalFailed ? 'Запустить новый разбор' : 'Разобрать задачу'}
        </button>
      </form>

      {analysisId && (
        <button className="workspace-retry" type="button" onClick={startNewTask}>Новая задача</button>
      )}

      <datalist id="server-issues">
        {issues.map((issue) => <option key={issue.task_id} value={issue.task_id}>{issue.title}</option>)}
      </datalist>
      {catalogError && (
        <div className="task-workspace__error" role="alert">
          <p>{catalogError}</p>
          <button className="workspace-retry" type="button" onClick={() => { setCatalogError(''); setCatalogAttempt(catalogAttempt + 1) }}>Повторить подключение</button>
        </div>
      )}
      {inputError && (
        <p className="task-workspace__error" id="task-input-error" role="alert">
          {inputError}
        </p>
      )}

      {inputError && analysisId && !isBusy && (
        <button className="workspace-retry" type="button" onClick={() => void refreshResult()}>Обновить результат</button>
      )}

      {/* До запуска разбора показываем приглашение ввести задачу. */}
      {!isAnalyzing && task === null && (
        <div className="workspace-placeholder">
          <span className="workspace-placeholder__symbol" aria-hidden="true">↗</span>
          <h2>Начнём с твоей задачи</h2>
          <p>
            Вставь ссылку Jira или выбери задачу из списка сервера,<br />
            затем нажми «Разобрать задачу».
          </p>
        </div>
      )}

      {isAnalyzing && (
        <div className="workspace-placeholder" role="status" aria-live="polite">
          <span className="workspace-placeholder__spinner" aria-hidden="true">
            <span />
            <span />
            <span />
            <span />
          </span>
          <h2>{loadingStatus === 'queued' ? 'Задача ожидает обработки…' : 'Собираю контекст задачи…'}</h2>
          <p>Описание задачи, материалы проекта и первые шаги</p>
        </div>
      )}

      {!isAnalyzing && task !== null && analysis !== null && analysis.result_revision !== null && (
        <section className="task-card" aria-labelledby="task-title">
          <header className="task-card__header">
            <div className="task-card__meta">
              <span className="task-card__key">{task.key}</span>
              <span className={statusClass}>{statusText}</span>
              <span>{analysis.execution_mode === 'replay' ? 'Записанный пример' : 'Live-анализ'}</span>
            </div>
            <h2 id="task-title">{task.title}</h2>
            {analysis.execution_mode === 'replay' && <p className="task-card__mode">Используется ранее записанный ответ.</p>}
          </header>

          <div className="workspace-tabs" role="tablist" aria-label="Разделы задачи">
            {tabs.map((tab, index) => (
              <button
                className="workspace-tabs__tab"
                type="button"
                role="tab"
                key={tab.id}
                id={`tab-${tab.id}`}
                aria-controls={`panel-${tab.id}`}
                aria-selected={activeTab === tab.id}
                tabIndex={activeTab === tab.id ? 0 : -1}
                ref={(button) => { tabButtons.current[index] = button }}
                onClick={() => setActiveTab(tab.id)}
                onKeyDown={(event) => handleTabKeyDown(event, index)}
              >
                {tab.title}
                {tab.id === 'steps' && (
                  <span className="workspace-tabs__count">{task.steps.length}</span>
                )}
                {tab.id === 'materials' && (
                  <span className="workspace-tabs__count">{task.sources.length}</span>
                )}
              </button>
            ))}
          </div>

          {/* hidden скрывает вкладку, сохраняя её черновик и выбранные материалы. */}
          <div
            role="tabpanel"
            id="panel-buddy"
            aria-labelledby="tab-buddy"
            hidden={activeTab !== 'buddy'}
          >
            <BuddyChat
              key={analysis.analysis_id}
              task={task}
              messages={messages}
              isSubmitting={isUpdatingPlan}
              onAnswer={answerClarification}
              onOpenSource={openSource}
            />
          </div>
          <div
            role="tabpanel"
            id="panel-steps"
            aria-labelledby="tab-steps"
            hidden={activeTab !== 'steps'}
          >
            <TaskSteps
              task={task}
              completedIds={completedStepIds}
              onToggle={toggleStep}
              saveStatus={saveStatus}
              onSave={savePlan}
              isReanalyzing={isUpdatingPlan}
              isBusy={isBusy}
              publicationLink={publicationUrl(analysis?.publication.url || '')}
              onOpenSource={openSource}
            />
          </div>
          <div
            role="tabpanel"
            id="panel-materials"
            aria-labelledby="tab-materials"
            hidden={activeTab !== 'materials'}
          >
            <TaskMaterials
              key={`${analysis.analysis_id}:${analysis.result_revision}`}
              sources={task.sources}
              analysisId={analysis.analysis_id}
              revision={analysis.result_revision}
              selected={selectedSource}
              onSelect={(id) => openSource(id)}
            />
          </div>
        </section>
      )}
    </section>
  )
}
