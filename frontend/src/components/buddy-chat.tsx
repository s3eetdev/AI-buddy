import { useId, useLayoutEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import type { ChatMessage, TaskContext } from '../types/task'
import './buddy-chat.css'

type BuddyChatProps = {
  task: TaskContext
  messages: ChatMessage[]
  isSubmitting: boolean
  onAnswer: (answer: string) => Promise<boolean>
  onOpenSource: (id: string, analysisId: string, revision: number) => void
}

const MAX_ANSWER_LENGTH = 2000

export function BuddyChat({ task, messages, isSubmitting, onAnswer, onOpenSource }: BuddyChatProps) {
  const [draft, setDraft] = useState('')
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const fieldId = useId()
  const questionsId = useId()
  const needsClarification = task.status === 'needs_clarification'
  const isAnswerDisabled = isSubmitting || draft.trim().length === 0
  useLayoutEffect(() => {
    const input = inputRef.current
    if (!input) return

    input.style.height = 'auto'
    input.style.height = `${input.scrollHeight}px`
  }, [draft, needsClarification])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const answer = draft.trim()

    if (!answer || isSubmitting || !needsClarification) return

    const accepted = await onAnswer(answer)
    if (accepted) setDraft('')
  }

  return (
    <section className="buddy-chat" aria-label="Уточнения по задаче">
      <div
        className="buddy-chat__conversation"
        role="log"
        aria-label="История уточнений с AI-buddy"
        aria-live="polite"
        aria-relevant="additions text"
        aria-atomic="false"
      >
        <ol className="buddy-chat__messages">
          {messages.map((message) => (
            <li
              key={message.id}
              className={`buddy-chat__message buddy-chat__message--${message.role}`}
            >
              {message.role === 'assistant' && (
                <div className="buddy-chat__sender">
                  <span className="buddy-chat__avatar" aria-hidden="true">
                    <span className="buddy-chat__robot">
                      <span className="buddy-chat__robot-eyes" />
                      <span className="buddy-chat__robot-mouth" />
                    </span>
                  </span>
                  <p className="buddy-chat__author">AI-buddy</p>
                </div>
              )}
              <div className="buddy-chat__message-content">
                {message.role === 'user' && (
                  <p className="buddy-chat__author">Вы</p>
                )}
                <div className="buddy-chat__bubble">
                  {message.text}
                  {message.findings?.map((finding, index) => (
                    <div className="buddy-chat__finding" key={index}>
                      <strong>{finding.kind === 'fact' ? 'Факт' : 'Гипотеза — требует проверки'}</strong>
                      <p>{finding.explanation}</p>
                      {finding.citations.map((citation, citationIndex) => (
                        <div className="buddy-chat__citation" key={citationIndex}>
                          <button className="source-link" type="button" onClick={() => onOpenSource(citation.source_id, message.analysisId, message.revision)}>
                            {citation.source_id} · строки {citation.start_line}–{citation.end_line}
                          </button>
                          <blockquote>{citation.quote}</blockquote>
                        </div>
                      ))}
                    </div>
                  ))}
                  {Boolean(message.limitations?.length) && (
                    <div className="buddy-chat__finding">
                      <strong>Что нужно учитывать</strong>
                      <ul>{message.limitations?.map((item, index) => <li key={index}>{item}</li>)}</ul>
                    </div>
                  )}
                </div>
              </div>
            </li>
          ))}
        </ol>
      </div>

      <div className="buddy-chat__reply-status" role="status" aria-live="polite">
        {isSubmitting && (
          <p className="buddy-chat__waiting">
            <span className="buddy-chat__waiting-dots" aria-hidden="true">
              <span /><span /><span />
            </span>
            Повторный разбор…
          </p>
        )}
      </div>

      {needsClarification ? (
        <form className="buddy-chat__composer" onSubmit={handleSubmit}>
          <div className="buddy-chat__questions" id={questionsId}>
            <h3 className="buddy-chat__questions-title">
              {task.openQuestions.length > 1 ? 'Уточнения Buddy' : 'Уточнение Buddy'}
            </h3>
            <ul className="buddy-chat__question-list">
              {task.openQuestions.map((question) => (
                <li key={question}>{question}</li>
              ))}
            </ul>
          </div>

          <label className="buddy-chat__label" htmlFor={fieldId}>
            Ответ на уточнение
          </label>
          <div className="buddy-chat__input-area">
            <textarea
              ref={inputRef}
              className="buddy-chat__input"
              id={fieldId}
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              maxLength={MAX_ANSWER_LENGTH}
              rows={3}
              placeholder="Ответ на вопрос помощника"
              disabled={isSubmitting}
              aria-describedby={questionsId}
            />
            <div className="buddy-chat__input-footer">
              <button
                className="buddy-chat__send"
                type="submit"
                disabled={isAnswerDisabled}
              >
                {isSubmitting ? 'Повторный разбор…' : 'Ответить и обновить план'}
              </button>
            </div>
          </div>
        </form>
      ) : (
        <div className="buddy-chat__outcome" role="status" aria-live="polite">
          {task.status === 'ready'
            ? 'План готов. Перейди во вкладку «Первые шаги».'
            : 'Для разбора недостаточно контекста. Проверьте описание и материалы задачи.'}
        </div>
      )}
    </section>
  )
}
