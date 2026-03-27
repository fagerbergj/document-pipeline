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
  getJobApiV1DocumentsDocIdJobsGet,
  postJobEventApiV1DocumentsDocIdJobsEventsPost,
  listJobEventsApiV1DocumentsDocIdJobsEventsGet,
  listContextsApiV1ContextsGet,
  createContextApiV1ContextsPost,
  updateContextApiV1ContextsContextIdPatch,
  deleteContextApiV1ContextsContextIdDelete,
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
  jobs: (params?: { stages?: string; states?: string; sort?: string; pageToken?: string; pageSize?: number }) =>
    unwrap(listJobsApiV1JobsGet({ query: {
      stages: params?.stages,
      states: params?.states,
      sort: params?.sort,
      pageToken: params?.pageToken,
      pageSize: params?.pageSize,
    }})),

  job: (id: string) =>
    unwrap(getJobApiV1DocumentsDocIdJobsGet({ path: { doc_id: id } })),

  postJobEvent: (
    id: string,
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
    unwrap(postJobEventApiV1DocumentsDocIdJobsEventsPost({ path: { doc_id: id }, body: event as never })),

  jobEvents: (id: string, pageSize = 100) =>
    unwrap(listJobEventsApiV1DocumentsDocIdJobsEventsGet({ path: { doc_id: id }, query: { pageSize } })),

  // ── Contexts ──────────────────────────────────────────────────────────────
  contexts: () =>
    unwrap(listContextsApiV1ContextsGet({})),

  createContext: (name: string, text: string) =>
    unwrap(createContextApiV1ContextsPost({ body: { name, text } })),

  updateContext: (id: string, patch: { name?: string | null; text?: string | null }) =>
    unwrap(updateContextApiV1ContextsContextIdPatch({ path: { context_id: id }, body: patch })),

  deleteContext: (id: string) =>
    unwrap(deleteContextApiV1ContextsContextIdDelete({ path: { context_id: id } })),

  // ── Chat (SSE — manual fetch needed for streaming) ────────────────────────
  chatStream: (
    messages: { role: string; content: string }[],
    context: string,
    topK: number,
    signal?: AbortSignal,
  ): Promise<Response> =>
    fetch('/api/v1/chats', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ messages, context, top_k: topK }),
      signal,
    }),
}
