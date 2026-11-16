import type { Analysis, ApiSource, Issue } from '../types/api'
import type { ChatMessage, TaskContext, TaskSource } from '../types/task'

export function sourceForScreen(source: ApiSource): TaskSource {
  return {
    id: source.id,
    title: source.path.split('/').pop() || source.id,
    kind: ['code', 'test', 'project'].includes(source.kind) ? 'project' : 'document',
    path: source.path,
    description: `${source.kind} · ${source.id}`,
    content: source.content,
  }
}

export function taskForScreen(analysis: Analysis, issues: Issue[]): TaskContext | null {
  if (!analysis.result || analysis.result_revision === null) return null
  const result = analysis.result
  const issue = issues.find((item) => item.task_id === analysis.task_id)
  return {
    key: analysis.task_id,
    title: issue?.title || analysis.task_id,
    status: result.status,
    description: result.plan.summary,
    steps: result.plan.steps.map((step, index) => ({
      id: `v${analysis.result_revision}-s${index + 1}`,
      title: step.action,
      description: step.expected_result,
      sourceIds: step.source_ids,
    })),
    sources: result.sources.map(sourceForScreen),
    openQuestions: result.plan.questions,
  }
}

export function messageForScreen(analysis: Analysis): ChatMessage {
  if (!analysis.result || analysis.result_revision === null) throw new Error('Сервер не вернул результат разбора.')
  return {
    id: `${analysis.analysis_id}-v${analysis.result_revision}`,
    role: 'assistant',
    analysisId: analysis.analysis_id,
    revision: analysis.result_revision,
    text: analysis.result.plan.summary,
    findings: analysis.result.plan.findings,
    limitations: analysis.result.plan.limitations,
  }
}
