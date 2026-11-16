import type { Finding } from './api'

export type WorkspaceTab = 'buddy' | 'steps' | 'materials'

export interface TaskSource {
  id: string
  title: string
  kind: 'project' | 'document'
  path: string
  description: string
  content: string
}

export interface TaskStep {
  id: string
  title: string
  description: string
  sourceIds: string[]
}

export interface TaskContext {
  key: string
  status: 'ready' | 'needs_clarification' | 'insufficient_context'
  title: string
  description: string
  steps: TaskStep[]
  sources: TaskSource[]
  openQuestions: string[]
}

export interface ChatMessage {
  id: string
  role: 'assistant' | 'user'
  analysisId: string
  revision: number
  text: string
  findings?: Finding[]
  limitations?: string[]
}
