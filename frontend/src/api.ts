/**
 * Thin wrapper around the generated SDK. Pages import from here for clean names.
 * All API calls use the generated functions so URLs/types stay in sync with openapi.yaml.
 * SSE streaming endpoints use raw fetch since they need manual stream handling.
 */
import {
  getPipelineApiV1PipelinesPipelineIdGet,
  getDocumentApiV1DocumentsDocIdGet,
  patchDocumentApiV1DocumentsDocIdPatch,
  deleteDocumentApiV1DocumentsDocIdDelete,
  listJobsApiV1JobsGet,
  getJobApiV1JobsJobIdGet,
  patchJobApiV1JobsJobIdPatch,
  postJobEventApiV1JobsJobIdEventsPost,
  listJobEventsApiV1JobsJobIdEventsGet,
  listContextsApiV1ContextsGet,
  createContextApiV1ContextsPost,
  updateContextApiV1ContextsContextIdPatch,
  deleteContextApiV1ContextsContextIdDelete,
  listChatSessionsApiV1ChatsGet,
  createChatSessionApiV1ChatsPost,
  getChatSessionApiV1ChatsSessionIdGet,
  patchChatSessionApiV1ChatsSessionIdPatch,
  deleteChatSessionApiV1ChatsSessionIdDelete,
} from './generated'

export type {
  DocumentDetail,
  DocumentSummary,
  JobDetail,
  JobSummary,
  PaginatedJobs,
  PaginatedContexts,
  PaginatedJobEvents,
  ContextEntry,
  PatchDocumentBody,
  CreateContextBody,
  UpdateContextBody,
  ReviewDetail,
  ClarificationRequest,
  StageDisplay,
  ReplayStage,
  JobEventRecord,
} from './generated'

export interface ChatSessionSummary {
  id: string
  title: string
  context: string
  top_k: number
  created_at: string
  updated_at: string
  message_count: number
}

export interface SourceDoc {
  doc_id: string
  title: string
  summary: string
  date_month: string
  score: number
}

export interface ChatMessageRecord {
  id: number
  role: 'user' | 'assistant'
  content: string
  sources?: SourceDoc[] | null
  created_at: string
}

export interface ChatSessionDetail extends ChatSessionSummary {
  messages: ChatMessageRecord[]
}

export interface PaginatedChatSessions {
  data: ChatSessionSummary[]
  next_before_id?: string | null
}

async function unwrap<T>(call: Promise<{ data?: T; error?: unknown }>): Promise<T> {
  const { data, error } = await call
  if (error) throw error
  return data as T
}

export const api = {
  // ── Pipelines ─────────────────────────────────────────────────────────────
  pipeline: (id = 'pipeline') =>
    unwrap(getPipelineApiV1PipelinesPipelineIdGet({ path: { pipeline_id: id } })),

  // ── Documents ─────────────────────────────────────────────────────────────
  document: (id: string) =>
    unwrap(getDocumentApiV1DocumentsDocIdGet({ path: { doc_id: id } })),

  updateDocument: (id: string, patch: { title?: string | null; document_context?: string | null; context_ref?: string | null }) =>
    unwrap(patchDocumentApiV1DocumentsDocIdPatch({ path: { doc_id: id }, body: patch })),

  deleteDocument: (id: string) =>
    unwrap(deleteDocumentApiV1DocumentsDocIdDelete({ path: { doc_id: id } })),

  // ── Jobs ──────────────────────────────────────────────────────────────────
  jobs: (params?: { documentId?: string; stages?: string; states?: string; sort?: string; pageToken?: string; pageSize?: number }) =>
    unwrap(listJobsApiV1JobsGet({ query: {
      documentId: params?.documentId,
      stages: params?.stages,
      states: params?.states,
      sort: params?.sort,
      pageToken: params?.pageToken,
      pageSize: params?.pageSize,
    }})),

  job: (jobId: string) =>
    unwrap(getJobApiV1JobsJobIdGet({ path: { job_id: jobId } })),

  updateJob: (jobId: string, patch: { embed_image?: boolean }) =>
    unwrap(patchJobApiV1JobsJobIdPatch({ path: { job_id: jobId }, body: patch })),

  postJobEvent: (
    jobId: string,
    event:
      | { type: 'approve'; edited_text?: string }
      | { type: 'reject' }
      | { type: 'clarify'; answers?: Record<string, string>; free_prompt?: string; edited_text?: string }
      | { type: 'retry' }
      | { type: 'stop' }
      | { type: 'replay'; stage: string }
      | { type: 'provide_context'; document_context?: string; context_ref?: string | null }
      | { type: 'clear_errors' },
  ) =>
    unwrap(postJobEventApiV1JobsJobIdEventsPost({ path: { job_id: jobId }, body: event as never })),

  jobEvents: (jobId: string, pageSize = 100) =>
    unwrap(listJobEventsApiV1JobsJobIdEventsGet({ path: { job_id: jobId }, query: { pageSize } })),

  // ── Contexts ──────────────────────────────────────────────────────────────
  contexts: () =>
    unwrap(listContextsApiV1ContextsGet({})),

  createContext: (name: string, text: string) =>
    unwrap(createContextApiV1ContextsPost({ body: { name, text } })),

  updateContext: (id: string, patch: { name?: string | null; text?: string | null }) =>
    unwrap(updateContextApiV1ContextsContextIdPatch({ path: { context_id: id }, body: patch })),

  deleteContext: (id: string) =>
    unwrap(deleteContextApiV1ContextsContextIdDelete({ path: { context_id: id } })),

  // ── Upload (FormData — bypasses generated client) ─────────────────────────
  uploadDocument: async (
    file: File,
    opts?: { title?: string; document_context?: string; context_ref?: string },
  ) => {
    const fd = new FormData()
    fd.append('file', file)
    if (opts?.title) fd.append('title', opts.title)
    if (opts?.document_context) fd.append('document_context', opts.document_context)
    if (opts?.context_ref) fd.append('context_ref', opts.context_ref)
    const res = await fetch('/api/v1/documents', { method: 'POST', body: fd })
    const json = await res.json()
    if (!res.ok) throw Object.assign(new Error(json.error ?? 'Upload failed'), { status: res.status, body: json })
    return json
  },

  // ── Chat sessions ─────────────────────────────────────────────────────────
  listChatSessions: (params?: { page_size?: number; before_id?: string }) =>
    unwrap(listChatSessionsApiV1ChatsGet({ query: { page_size: params?.page_size, before_id: params?.before_id } })) as Promise<PaginatedChatSessions>,

  createChatSession: (opts?: { context?: string; top_k?: number }) =>
    unwrap(createChatSessionApiV1ChatsPost({ body: { context: opts?.context ?? '', top_k: opts?.top_k ?? 5 } })) as Promise<ChatSessionSummary>,

  getChatSession: (sessionId: string) =>
    unwrap(getChatSessionApiV1ChatsSessionIdGet({ path: { session_id: sessionId } })) as Promise<ChatSessionDetail>,

  patchChatSession: (sessionId: string, patch: { title?: string | null; context?: string | null; top_k?: number | null }) =>
    unwrap(patchChatSessionApiV1ChatsSessionIdPatch({ path: { session_id: sessionId }, body: patch })) as Promise<ChatSessionSummary>,

  deleteChatSession: (sessionId: string) =>
    unwrap(deleteChatSessionApiV1ChatsSessionIdDelete({ path: { session_id: sessionId } })),

  // ── Chat message streaming (SSE — manual fetch needed) ────────────────────
  sendMessage: (sessionId: string, content: string, signal?: AbortSignal): Promise<Response> =>
    fetch(`/api/v1/chats/${sessionId}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content }),
      signal,
    }),
}
