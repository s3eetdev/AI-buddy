import { useEffect, useId, useState } from 'react'
import { errorMessage, getSource } from '../api/task-api'
import { sourceForScreen } from '../api/task-view'
import type { TaskSource } from '../types/task'
import './task-materials.css'

interface TaskMaterialsProps {
  sources: TaskSource[]
  analysisId: string
  revision: number
  selected: { id: string; analysisId: string; revision: number } | null
  onSelect: (id: string) => void
}
const groups: { kind: TaskSource['kind']; title: string; caption: string }[] = [
  { kind: 'project', title: 'Проекты', caption: 'Код и тесты' },
  { kind: 'document', title: 'Документы', caption: 'Инструкции, контракты и описание задачи' },
]

function SourcePreview({ analysisId, revision, sourceId, onRetry }: {
  analysisId: string; revision: number; sourceId: string; onRetry: () => void
}) {
  const [preview, setPreview] = useState<TaskSource | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    getSource(analysisId, sourceId, revision, controller.signal)
      .then((source) => { if (!controller.signal.aborted) setPreview(sourceForScreen(source)) })
      .catch((reason) => { if (!controller.signal.aborted) setError(errorMessage(reason)) })
    return () => controller.abort()
  }, [analysisId, revision, sourceId])

  if (error) return <div role="alert"><p>{error}</p><button className="workspace-retry" type="button" onClick={onRetry}>Повторить загрузку</button></div>
  if (!preview) return <p role="status">Загружаем материал…</p>
  return (
    <article>
      <h3>{preview.title}</h3>
      <p className="task-materials__path-label">Путь в проекте</p>
      <code className="task-materials__path">{preview.path}</code>
      <pre className="task-materials__content">{preview.content}</pre>
    </article>
  )
}

export function TaskMaterials({ sources, analysisId, revision, selected, onSelect }: TaskMaterialsProps) {
  const [attempt, setAttempt] = useState(0)
  const previewId = useId()

  return (
    <section className="task-materials" aria-labelledby="task-materials-title">
      <div className="task-materials__heading">
        <h2 id="task-materials-title">Всё нужное по задаче</h2>
        <p>Проекты и документы, на которые опирается разбор.</p>
      </div>
      <div className="task-materials__layout">
        <div className="task-materials__groups">
          {groups.map((group) => {
            const items = sources.filter((source) => source.kind === group.kind)
            return (
              <section className="task-materials__group" key={group.kind}>
                <div className="task-materials__group-heading">
                  <div><h3>{group.title}</h3><p>{group.caption}</p></div>
                  <span className="task-materials__count">{items.length}</span>
                </div>
                <ul className="task-materials__list">
                  {items.map((source) => (
                    <li key={source.id}>
                      <button
                        className={'task-materials__source' + (source.id === selected?.id && analysisId === selected.analysisId && revision === selected.revision ? ' task-materials__source--selected' : '')}
                        type="button"
                        aria-pressed={source.id === selected?.id && analysisId === selected.analysisId && revision === selected.revision}
                        aria-controls={previewId}
                        onClick={() => onSelect(source.id)}
                      >
                        <span className="task-materials__source-icon" aria-hidden="true">{source.kind === 'project' ? '</>' : '≡'}</span>
                        <span className="task-materials__source-copy">
                          <span className="task-materials__source-title">{source.title}</span>
                          <span className="task-materials__source-description">{source.description}</span>
                        </span>
                        <span className="task-materials__source-arrow" aria-hidden="true">→</span>
                      </button>
                    </li>
                  ))}
                </ul>
                {items.length === 0 && <p className="task-materials__empty-group">Доступных материалов нет.</p>}
              </section>
            )
          })}
        </div>
        <div className="task-materials__preview" id={previewId}>
          {selected && selected.revision !== revision && <p>Источник из версии результата {selected.revision}.</p>}
          {selected && (
            <SourcePreview
              key={`${selected.analysisId}:${selected.revision}:${selected.id}:${attempt}`}
              analysisId={selected.analysisId}
              revision={selected.revision}
              sourceId={selected.id}
              onRetry={() => setAttempt(attempt + 1)}
            />
          )}
          {!selected && (
            <div className="task-materials__preview-empty">
              <span className="task-materials__empty-icon" aria-hidden="true">≡</span>
              <h3>Посмотрите материал</h3>
              <p>Выберите источник слева или откройте ссылку из ответа Buddy.</p>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
