export interface DocumentSummary {
  id: string
  title: string | null
  current_stage: string
  stage_state: 'pending' | 'running' | 'waiting' | 'error' | 'done'
  created_at: string
  updated_at: string
  needs_context: boolean
}

export interface ClarificationRequest {
  segment: string
  question: string
}

export interface Review {
  stage_name: string
  input_field: string | null
  output_field: string | null
  input_text: string
  output_text: string
  is_single_output: boolean
  confidence: string
  qa_rounds: number
  clarification_requests: ClarificationRequest[]
}

export interface StageDisplay {
  name: string
  fields: Record<string, string>
}

export interface StageEvent {
  timestamp: string
  stage: string
  event_type: string
  data: { error?: string } | null
}

export interface DocumentDetail {
  id: string
  title: string | null
  current_stage: string
  stage_state: string
  created_at: string
  updated_at: string
  document_context: string
  context_required: boolean
  stage_displays: StageDisplay[]
  review: Review | null
  replay_stages: { name: string }[]
  events: StageEvent[]
}

export interface ContextEntry {
  name: string
  text: string
}

export interface Counts {
  pending?: number
  running?: number
  waiting?: number
  error?: number
  done?: number
  by_stage?: Record<string, number>
}
