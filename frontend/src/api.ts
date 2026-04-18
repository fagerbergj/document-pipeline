/**
 * Thin wrapper around the generated SDK. Pages import from here for clean names.
 * All API calls use the generated functions so URLs/types stay in sync with openapi.yaml.
 * SSE streaming endpoints use raw fetch since they need manual stream handling.
 */
import {
  getPipeline as getPipelineApiV1PipelinesPipelineIdGet,
  getDocument as getDocumentApiV1DocumentsDocIdGet,
  patchDocument as patchDocumentApiV1DocumentsDocIdPatch,
  deleteDocument as deleteDocumentApiV1DocumentsDocIdDelete,
  listJobs as listJobsApiV1JobsGet,
  getJob as getJobApiV1JobsJobIdGet,
  patchJob as patchJobApiV1JobsJobIdPatch,
  putJobStatus as putJobStatusApiV1JobsJobIdStatusPut,
  patchRun as patchRunApiV1JobsJobIdRunsRunIdPatch,
  listContexts as listContextsApiV1ContextsGet,
  createContext as createContextApiV1ContextsPost,
  updateContext as updateContextApiV1ContextsContextIdPatch,
  deleteContext as deleteContextApiV1ContextsContextIdDelete,
  listChats as listChatsApiV1ChatsGet,
  createChat as createChatApiV1ChatsPost,
  getChat as getChatApiV1ChatsChatIdGet,
  patchChat as patchChatApiV1ChatsChatIdPatch,
  deleteChat as deleteChatApiV1ChatsChatIdDelete,
} from './generated'

export type {
  DocumentDetail,
  DocumentSummary,
  JobDetail,
  JobSummary,
  JobStatus,
  JobOptions,
  PaginatedJobs,
  PaginatedContexts,
  ContextEntry,
  PatchDocumentBody,
  CreateContextBody,
  UpdateContextBody,
  Artifact,
  Run,
  RunIoField,
  RunQuestion,
  RunSuggestions,
  PipelineDetail,
  StageSummary,
  StageDetail,
} from './generated'

// DocumentSummary enriched with current job stage/status (backend adds these fields)
export interface DocSummary {
  id: string
  title: string | null
  series: string | null
  current_job_id: string | null
  created_at: string
  updated_at: string
}

// Chat types (generator returns unknown for these responses)
export interface SourceDoc {
  document_id: string
  title: string | null
  summary: string | null
  date_month: string | null
  score: number
}

export interface ChatMessage {
  id: string
  external_id?: string | null
  role: 'user' | 'assistant'
  content: string
  sources?: SourceDoc[] | null
  created_at: string
}

export interface RagRetrieval {
  enabled?: boolean | null
  max_sources?: number | null
  minimum_score?: number | null
}

export interface ChatSummary {
  id: string
  title: string | null
  system_prompt: string | null
  rag_retrieval: RagRetrieval | null
  created_at: string
  updated_at: string
}

export interface ChatDetail extends ChatSummary {
  messages: ChatMessage[]
}

export interface PaginatedChats {
  data: ChatSummary[]
  next_page_token?: string | null
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
  documents: async (params?: { sort?: string; page_size?: number; page_token?: string; stages?: string; statuses?: string }): Promise<{ data: DocSummary[]; next_page_token?: string | null }> => {
    const q = new URLSearchParams()
    if (params?.sort)       q.set('sort', params.sort)
    if (params?.page_size)  q.set('page_size', String(params.page_size))
    if (params?.page_token) q.set('page_token', params.page_token)
    if (params?.stages)     q.set('stages', params.stages)
    if (params?.statuses)   q.set('statuses', params.statuses)
    const res = await fetch(`/api/v1/documents?${q}`)
    const json = await res.json()
    if (!res.ok) throw Object.assign(new Error(json.error ?? 'Failed'), { status: res.status, body: json })
    return json
  },

  document: (id: string) =>
    unwrap(getDocumentApiV1DocumentsDocIdGet({ path: { doc_id: id } })),

  updateDocument: (id: string, patch: { title?: string | null; additional_context?: string | null; linked_contexts?: string[] | null; series?: string | null }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(patchDocumentApiV1DocumentsDocIdPatch({ path: { doc_id: id }, body: patch as any })),

  deleteDocument: (id: string) =>
    unwrap(deleteDocumentApiV1DocumentsDocIdDelete({ path: { doc_id: id } })),

  // ── Jobs ──────────────────────────────────────────────────────────────────
  jobs: (params?: { job_id?: string; document_id?: string; stages?: string; statuses?: string; sort?: string; page_token?: string; page_size?: number }) =>
    unwrap(listJobsApiV1JobsGet({ query: {
      job_id: params?.job_id,
      document_id: params?.document_id,
      stages: params?.stages,
      statuses: params?.statuses,
      sort: params?.sort as 'pipeline' | 'title_asc' | 'title_desc' | 'created_asc' | 'created_desc' | undefined,
      page_token: params?.page_token,
      page_size: params?.page_size,
    }})),

  job: (jobId: string) =>
    unwrap(getJobApiV1JobsJobIdGet({ path: { job_id: jobId } })),

  updateJob: (jobId: string, patch: { options?: { require_context?: boolean; embed?: { embed_image?: boolean } } | null }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(patchJobApiV1JobsJobIdPatch({ path: { job_id: jobId }, body: patch as any })),

  putJobStatus: (jobId: string, status: 'pending' | 'done' | 'error') =>
    unwrap(putJobStatusApiV1JobsJobIdStatusPut({ path: { job_id: jobId }, body: { status } })),

  patchRun: (jobId: string, runId: string, patch: { questions?: { segment: string; question: string; answer?: string | null }[] | null }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(patchRunApiV1JobsJobIdRunsRunIdPatch({ path: { job_id: jobId, run_id: runId }, body: patch as any })),

  // ── Contexts ──────────────────────────────────────────────────────────────
  contexts: () =>
    unwrap(listContextsApiV1ContextsGet({})),

  createContext: (name: string, text: string) =>
    unwrap(createContextApiV1ContextsPost({ body: { name, text } })),

  updateContext: (id: string, patch: { name?: string | null; text?: string | null }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(updateContextApiV1ContextsContextIdPatch({ path: { context_id: id }, body: patch as any })),

  deleteContext: (id: string) =>
    unwrap(deleteContextApiV1ContextsContextIdDelete({ path: { context_id: id } })),

  // ── Upload (FormData — bypasses generated client) ─────────────────────────
  uploadDocument: async (
    file: File,
    opts?: { title?: string; additional_context?: string; linked_contexts?: string[]; series?: string },
  ) => {
    const fd = new FormData()
    fd.append('file', file)
    if (opts?.title) fd.append('title', opts.title)
    if (opts?.additional_context) fd.append('additional_context', opts.additional_context)
    if (opts?.linked_contexts?.length) fd.append('linked_contexts', opts.linked_contexts.join(','))
    if (opts?.series) fd.append('series', opts.series)
    const res = await fetch('/api/v1/documents', { method: 'POST', body: fd })
    const json = await res.json()
    if (!res.ok) throw Object.assign(new Error(json.error ?? 'Upload failed'), { status: res.status, body: json })
    return json as { id: string; document_id: string }
  },

  // ── Chats ─────────────────────────────────────────────────────────────────
  listChats: (params?: { page_size?: number; before_id?: string }) =>
    unwrap(listChatsApiV1ChatsGet({ query: { page_size: params?.page_size, before_id: params?.before_id } })) as Promise<PaginatedChats>,

  createChat: (opts?: { system_prompt?: string; rag_retrieval?: RagRetrieval }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(createChatApiV1ChatsPost({ body: { system_prompt: opts?.system_prompt, rag_retrieval: opts?.rag_retrieval } as any })) as Promise<ChatSummary>,

  getChat: (chatId: string) =>
    unwrap(getChatApiV1ChatsChatIdGet({ path: { chat_id: chatId } })) as Promise<ChatDetail>,

  patchChat: (chatId: string, patch: { title?: string | null; system_prompt?: string | null; rag_retrieval?: RagRetrieval | null }) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    unwrap(patchChatApiV1ChatsChatIdPatch({ path: { chat_id: chatId }, body: patch as any })) as Promise<ChatSummary>,

  deleteChat: (chatId: string) =>
    unwrap(deleteChatApiV1ChatsChatIdDelete({ path: { chat_id: chatId } })),

  // ── Chat message streaming (SSE — manual fetch needed) ────────────────────
  sendMessage: (chatId: string, content: string, signal?: AbortSignal): Promise<Response> =>
    fetch(`/api/v1/chats/${chatId}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content }),
      signal,
    }),
}
