import type { TaskContext } from '../types/task'
import './task-steps.css'

interface TaskStepsProps {
  task: TaskContext
  completedIds: string[]
  onToggle: (id: string) => void
  saveStatus: 'idle' | 'saving' | 'saved' | 'error'
  onSave: () => void
  isReanalyzing: boolean
  isBusy: boolean
  publicationLink: string
  onOpenSource: (id: string) => void
}

export function TaskSteps({
  task,
  completedIds,
  onToggle,
  saveStatus,
  onSave,
  isReanalyzing,
  isBusy,
  publicationLink,
  onOpenSource,
}: TaskStepsProps) {
  const completedCount = task.steps.filter((step) =>
    completedIds.includes(step.id),
  ).length

  const saveMessage = {
    idle: isReanalyzing
      ? 'План обновляется…'
      : task.status === 'ready'
        ? 'План можно сохранить как черновик.'
        : task.status === 'needs_clarification'
          ? 'Сохранение доступно после обработки уточнений.'
          : 'Сохранение недоступно: недостаточно материалов.',
    saving: 'Сохраняем план…',
    saved: 'План сохранён в тестовой Jira.',
    error: 'Не удалось сохранить план. Попробуйте ещё раз.',
  }[saveStatus]

  return (
    <section className="task-steps" aria-labelledby="task-steps-title">
      <div className="task-steps__heading">
        <div>
          <h2 id="task-steps-title">С чего начать</h2>
          <p>Небольшой план, чтобы перейти от задачи к коду.</p>
        </div>
        <span className="task-steps__progress">
          {completedCount} из {task.steps.length}
        </span>
      </div>

      <ol className="task-steps__list">
        {task.steps.map((step, index) => {
          const isCompleted = completedIds.includes(step.id)

          return (
            <li
              className={
                'task-steps__item' +
                (isCompleted ? ' task-steps__item--completed' : '')
              }
              key={step.id}
            >
              <label className="task-steps__step">
                <input
                  className="task-steps__checkbox"
                  type="checkbox"
                  checked={isCompleted}
                  disabled={isBusy}
                  onChange={() => onToggle(step.id)}
                />
                <span className="task-steps__number" aria-hidden="true">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <span className="task-steps__copy">
                  <span className="task-steps__title">{step.title}</span>
                  <span className="task-steps__description">
                    {step.description}
                  </span>
                </span>
              </label>
              {step.sourceIds.length > 0 && (
                <div className="task-steps__sources">
                  {step.sourceIds.map((id) => (
                    <button className="source-link" key={id} type="button" onClick={() => onOpenSource(id)}>
                      {task.sources.find((source) => source.id === id)?.title || id}
                    </button>
                  ))}
                </div>
              )}
            </li>
          )
        })}
      </ol>

      {task.openQuestions.length > 0 && (
        <aside className="task-steps__question">
          <span className="task-steps__question-icon" aria-hidden="true">?</span>
          <div>
            <h3>Нужно уточнить</h3>
            {task.openQuestions.map((question) => (
              <p key={question}>{question}</p>
            ))}
          </div>
        </aside>
      )}

      <div className="task-steps__save-panel">
        <div className="task-steps__draft">
          <div className="task-steps__draft-heading">
            <h3>Черновик плана</h3>
          </div>
          <p className="task-steps__draft-task">
            {task.key} · {task.title}
          </p>
          <p
            className={
              'task-steps__save-status' +
              (saveStatus === 'error' ? ' task-steps__save-status--error' : '')
            }
            role="status"
            aria-live="polite"
          >
            {saveMessage}
          </p>
        </div>
        <div className="task-steps__save-action">
          <button
            className="task-steps__save-button"
            type="button"
            disabled={task.status !== 'ready' || isBusy || saveStatus === 'saving' || saveStatus === 'saved'}
            onClick={onSave}
          >
            {saveStatus === 'saving'
              ? 'Сохраняем…'
              : saveStatus === 'saved'
                ? 'План сохранён'
                : 'Сохранить в Jira'}
          </button>
          {publicationLink && <p><a href={publicationLink} target="_blank" rel="noreferrer">Открыть сохранённый план</a></p>}
          <p>{isReanalyzing ? 'Дождитесь окончания повторного разбора.' : task.status === 'needs_clarification' ? 'Сначала ответьте на уточнение во вкладке AI-buddy.' : task.status === 'insufficient_context' ? 'Нужны дополнительные материалы.' : 'По нажатию план будет сохранён в тестовой Jira.'}</p>
        </div>
      </div>
    </section>
  )
}
