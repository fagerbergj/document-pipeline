import type { Counts, DocumentSummary, DocumentDetail, ContextEntry } from './types'

const BASE = ''

async function apiFetch(path: string, options?: RequestInit) {
  const res = await fetch(BASE + path, options)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export const api = {
  stages: (): Promise<{ stages: string[] }> => apiFetch('/api/v1/pipeline/stages'),
  counts: (): Promise<Counts> => apiFetch('/api/v1/counts'),
  documents: (params?: { stages?: string; states?: string; sort?: string }): Promise<DocumentSummary[]> => {
    const q = new URLSearchParams()
    if (params?.stages) q.set('stages', params.stages)
    if (params?.states) q.set('states', params.states)
    if (params?.sort) q.set('sort', params.sort)
    return apiFetch(`/api/v1/documents?${q}`)
  },
  document: (id: string): Promise<DocumentDetail> => apiFetch(`/api/v1/documents/${id}`),
  updateTitle: (id: string, title: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/title`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ title }) }),
  saveContext: (id: string, ctx: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/context`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ document_context: ctx }) }),
  setContext: (id: string, ctx: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/set-context`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ document_context: ctx }) }),
  approve: (id: string, editedText?: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/approve`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ edited_text: editedText ?? '' }) }),
  reject: (id: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/reject`, { method: 'POST' }),
  clarify: (id: string, answers: Record<string, string>, free_prompt: string, edited_text?: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/clarify`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ answers, free_prompt, edited_text: edited_text ?? '' }) }),
  stop: (id: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/stop`, { method: 'POST' }),
  retry: (id: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/retry`, { method: 'POST' }),
  replay: (id: string, stage: string): Promise<DocumentDetail> =>
    apiFetch(`/api/v1/documents/${id}/replay/${stage}`, { method: 'POST' }),
  deleteDocument: (id: string): Promise<{ ok: boolean }> =>
    apiFetch(`/api/v1/documents/${id}`, { method: 'DELETE' }),
  contextLibrary: (): Promise<ContextEntry[]> => apiFetch('/api/v1/context-library'),
  saveContextEntry: (name: string, text: string): Promise<ContextEntry[]> =>
    apiFetch('/api/v1/context-library', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, text }) }),
  deleteContextEntry: (name: string): Promise<ContextEntry[]> =>
    apiFetch(`/api/v1/context-library/${encodeURIComponent(name)}`, { method: 'DELETE' }),
}
