// Названия полей совпадают с контрактом Go: specs/backend-openapi.yaml.
export type AnalysisStatus = 'queued' | 'collecting_context' | 'ready' | 'needs_clarification' | 'insufficient_context' | 'failed'
export interface ApiSource { id: string; scope: string; path: string; kind: string; content: string }
export interface Finding {
  kind: 'fact' | 'hypothesis'
  role: string
  explanation: string
  citations: { source_id: string; start_line: number; end_line: number; quote: string }[]
}
export interface AnalysisResult {
  task_id: string
  status: 'ready' | 'needs_clarification' | 'insufficient_context'
  plan: {
    summary: string
    findings: Finding[]
    steps: { action: string; source_ids: string[]; expected_result: string }[]
    questions: string[]
    limitations: string[]
  }
  sources: ApiSource[]
}
export interface Publication { status: 'not_published' | 'publishing' | 'published' | 'publish_failed'; plan_id?: string; url?: string }
export interface Analysis {
  analysis_id: string
  task_id: string
  status: AnalysisStatus
  execution_mode: 'live' | 'replay'
  result_revision: number | null
  result: AnalysisResult | null
  step_progress: { step_id: string; completed: boolean; result_revision?: number }[]
  publication: Publication
  error: { code: string; message: string } | null
}
export interface Issue { task_id: string; title: string; jira_url: string }
export interface QueuedAnalysis { analysis_id: string; status: AnalysisStatus; status_url: string }
