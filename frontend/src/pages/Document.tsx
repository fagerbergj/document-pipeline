import { useState, useEffect, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { api } from '../api'
import type { DocumentDetail, JobDetail, Run, Artifact } from '../types'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import DocKebabMenu from '../components/DocKebabMenu'

export default function Document() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const navigate = useNavigate()

  const { data: doc, isLoading: docLoading, error: docError } = useQuery({
    queryKey: ['document', id],
    queryFn: () => api.document(id!),
    retry: 1,
  })

  const jobId = doc?.current_job_id ?? null

  const { data: job, isLoading: jobLoading } = useQuery({
    queryKey: ['job', jobId],
    queryFn: () => api.job(jobId!),
    enabled: !!jobId,
    retry: 1,
  })

  // All jobs for this document (for replay UI)
  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-for-doc', id],
    queryFn: () => api.jobs({ document_id: id! }),
    enabled: !!id,
  })
  const allJobs = jobsPage?.data ?? []

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['document', id] })
    qc.invalidateQueries({ queryKey: ['job', jobId] })
    qc.invalidateQueries({ queryKey: ['jobs-for-doc', id] })
    qc.invalidateQueries({ queryKey: ['jobs'] })
  }

  const handleDelete = () => navigate('/')

  const isLoading = docLoading || (!!jobId && jobLoading)

  if (isLoading) return (
    <div className="flex items-center justify-center h-full py-24">
      <LoadingSpinner />
    </div>
  )
  if (!doc) {
    const errMsg = docError instanceof Error ? docError.message : null
    return (
      <div className="flex flex-col items-center justify-center h-full py-24 gap-2 text-gray-400">
        <div>{errMsg ? 'Failed to load document' : 'Document not found'}</div>
        {errMsg && <div className="text-xs font-mono text-red-500 max-w-lg text-center break-all">{errMsg}</div>}
      </div>
    )
  }

  const latestRun = job?.runs && job.runs.length > 0 ? job.runs[job.runs.length - 1] : null

  return (
    <div>
      {/* Header bar */}
      <div className="sticky top-0 z-10 flex items-center gap-3 px-6 py-4 border-b border-gray-200 bg-white">
        <Link to="/" className="text-gray-400 hover:text-gray-600 text-sm">←</Link>
        <h1 className="text-base font-semibold text-gray-900 flex-1 truncate">{doc.title || '(untitled)'}</h1>
        {job && (
          <>
            <span className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-0.5 rounded">{job.stage}</span>
            <StatusBadge state={job.status} />
          </>
        )}
        <DocKebabMenu
          docId={doc.id}
          jobId={job?.id}
          status={job?.status ?? 'pending'}
          onDelete={handleDelete}
          onSuccess={refresh}
        />
      </div>

      {/* Content */}
      <div className="p-6 space-y-4">
        <TitleSection doc={doc} onRefresh={refresh} />
        <ContextSection doc={doc} onRefresh={refresh} />
        {job?.status === 'running' && <LiveLogSection jobId={job.id} onDone={refresh} />}
        {job?.status === 'waiting' && latestRun && (
          <ReviewSection job={job} run={latestRun} doc={doc} onRefresh={refresh} />
        )}
        {job?.status === 'error' && (
          <ErrorSection job={job} onRefresh={refresh} />
        )}
        {(doc.artifacts?.length ?? 0) > 0 && (
          <ArtifactsSection doc={doc} />
        )}
        {job && job.options?.embed !== undefined && (
          <EmbedImageSection job={job} onRefresh={refresh} />
        )}
        {allJobs.length > 0 && (
          <JobsSection jobs={allJobs} currentJobId={jobId ?? undefined} onRefresh={refresh} />
        )}
      </div>
    </div>
  )
}

function TitleSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [title, setTitle] = useState(doc.title ?? '')
  const mut = useMutation({
    mutationFn: (t: string) => api.updateDocument(doc.id, { title: t }),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-3">Title</div>
      <form onSubmit={e => { e.preventDefault(); mut.mutate(title) }} className="flex gap-2">
        <input value={title} onChange={e => setTitle(e.target.value)}
          className="flex-1 text-sm border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200" />
        <button type="submit" disabled={mut.isPending}
          className="px-4 py-2 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 disabled:opacity-50">
          Save
        </button>
      </form>
      <div className="text-xs text-gray-400 mt-2">Received {doc.created_at.slice(0, 19).replace('T', ' ')}</div>
    </div>
  )
}

function ContextSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [editing, setEditing] = useState(false)
  const [ctx, setCtx] = useState(doc.additional_context ?? '')
  const [linkedIds, setLinkedIds] = useState<string[]>(doc.linked_contexts ?? [])
  const [entries, setEntries] = useState<{ id: string; name: string; text: string }[]>([])

  const hasContext = !!(doc.additional_context || (doc.linked_contexts?.length ?? 0) > 0)

  useEffect(() => {
    if (editing || hasContext) {
      api.contexts().then(p => setEntries(p.data ?? [])).catch(() => {})
    }
  }, [editing, hasContext])

  function openEdit() {
    setCtx(doc.additional_context ?? '')
    setLinkedIds(doc.linked_contexts ?? [])
    setEditing(true)
  }

  const saveMut = useMutation({
    mutationFn: () => api.updateDocument(doc.id, {
      additional_context: ctx || null,
      linked_contexts: linkedIds.length > 0 ? linkedIds : null,
    }),
    onSuccess: () => { onRefresh(); setEditing(false) },
  })

  function toggleLinked(id: string) {
    setLinkedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  // Collapsed pill when context is set and not editing
  if (hasContext && !editing) {
    const parts: string[] = []
    if ((doc.linked_contexts?.length ?? 0) > 0) {
      const names = (doc.linked_contexts ?? []).map(id => {
        const entry = entries.find(e => e.id === id)
        return entry ? `↗ ${entry.name}` : `↗ ref:${id.slice(0, 8)}…`
      })
      parts.push(...names)
    }
    if (doc.additional_context) {
      const preview = doc.additional_context.split('\n')[0].slice(0, 48)
      parts.push(preview + (doc.additional_context.length > 48 || doc.additional_context.includes('\n') ? '…' : ''))
    }
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-gray-400 uppercase tracking-wide font-semibold">Context</span>
        <button onClick={openEdit}
          className="inline-flex items-center gap-1 px-2.5 py-1 bg-gray-100 hover:bg-gray-200 text-gray-600 text-xs rounded-full transition-colors max-w-sm truncate">
          <span className="truncate">{parts.join(' · ')}</span>
          <span className="text-gray-400 flex-shrink-0">✎</span>
        </button>
      </div>
    )
  }

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Document context</div>
        {hasContext && (
          <button onClick={() => setEditing(false)} className="text-xs text-gray-400 hover:text-gray-600">Cancel</button>
        )}
      </div>

      {/* Linked contexts */}
      {entries.length > 0 && (
        <div className="mb-3">
          <label className="block text-xs text-gray-500 mb-1">Linked contexts</label>
          <div className="space-y-1">
            {entries.map(e => (
              <label key={e.id} className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={linkedIds.includes(e.id)}
                  onChange={() => toggleLinked(e.id)}
                  className="rounded border-gray-300"
                />
                <span className="text-sm text-gray-700">{e.name}</span>
              </label>
            ))}
          </div>
        </div>
      )}
      {entries.length === 0 && (
        <div className="mb-3 text-xs text-gray-400">
          No saved contexts —{' '}
          <Link to="/contexts" className="text-blue-500 hover:underline">create one in Context Library</Link>
        </div>
      )}

      {/* Free-text additional context */}
      <div>
        <label className="block text-xs text-gray-500 mb-1">Additional context</label>
        <textarea value={ctx} onChange={e => setCtx(e.target.value)} rows={4}
          className="w-full text-sm font-mono border border-gray-200 rounded-lg px-3 py-2 resize-y focus:outline-none focus:ring-2 focus:ring-blue-200"
          placeholder="Describe this document — used by clarify, classify, and other stages…" />
      </div>

      <div className="flex gap-2 mt-3">
        <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}
          className="px-3 py-1.5 text-xs font-medium border border-gray-300 rounded-lg hover:bg-gray-50 disabled:opacity-50">
          Save
        </button>
      </div>
    </div>
  )
}

function ArtifactsSection({ doc }: { doc: DocumentDetail }) {
  const artifacts = doc.artifacts ?? []
  const [activeId, setActiveId] = useState(artifacts[0]?.id ?? '')
  const activeArtifact = artifacts.find(a => a.id === activeId)

  if (artifacts.length === 0) return null

  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
      <div className="flex border-b border-gray-100 overflow-x-auto">
        {artifacts.map(a => (
          <button key={a.id} onClick={() => setActiveId(a.id)}
            className={`px-4 py-2.5 text-xs font-medium whitespace-nowrap transition-colors border-b-2 -mb-px ${
              activeId === a.id
                ? 'border-gray-900 text-gray-900'
                : 'border-transparent text-gray-400 hover:text-gray-600'
            }`}>
            {a.filename}
          </button>
        ))}
      </div>
      <div className="p-4">
        {activeArtifact && <ArtifactViewer doc={doc} artifact={activeArtifact} />}
      </div>
    </div>
  )
}

function ArtifactViewer({ doc, artifact }: { doc: DocumentDetail; artifact: Artifact }) {
  const [raw, setRaw] = useState(false)
  const url = `/api/v1/documents/${doc.id}/artifacts/${artifact.id}`

  if (artifact.content_type.startsWith('image/')) {
    return (
      <div>
        <div className="flex justify-end mb-2">
          <a href={url} target="_blank" rel="noreferrer"
            className="text-xs text-gray-400 hover:text-gray-600">Open in new tab ↗</a>
        </div>
        <img src={url} alt={artifact.filename}
          className="max-w-full rounded-lg border border-gray-100" />
      </div>
    )
  }

  if (artifact.content_type.startsWith('text/')) {
    return (
      <TextArtifact url={url} filename={artifact.filename} raw={raw} onToggleRaw={() => setRaw(r => !r)} />
    )
  }

  return (
    <div className="text-xs text-gray-500">
      <a href={url} target="_blank" rel="noreferrer" className="text-blue-500 hover:underline">
        Download {artifact.filename} ↗
      </a>
    </div>
  )
}

function TextArtifact({ url, filename, raw, onToggleRaw }: {
  url: string; filename: string; raw: boolean; onToggleRaw: () => void
}) {
  const [text, setText] = useState<string | null>(null)
  useEffect(() => {
    fetch(url).then(r => r.text()).then(setText).catch(() => setText('(error loading artifact)'))
  }, [url])

  const isMarkdown = text ? (text.includes('\n') && /[#*`\-]/.test(text)) : false

  if (text === null) return <div className="text-xs text-gray-400">Loading…</div>

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">{filename}</div>
        <div className="flex items-center gap-3">
          {isMarkdown && (
            <button onClick={onToggleRaw} className="text-xs text-gray-400 hover:text-gray-600">
              {raw ? 'Rendered' : 'Raw'}
            </button>
          )}
          <button onClick={() => {
            const blob = new Blob([text], { type: 'text/plain' })
            window.open(URL.createObjectURL(blob), '_blank')
          }} className="text-xs text-gray-400 hover:text-gray-600">Open in new tab ↗</button>
        </div>
      </div>
      {isMarkdown && !raw ? (
        <div className="prose prose-sm prose-gray max-w-none bg-gray-50 border border-gray-100 rounded-lg px-4 py-3 max-h-96 overflow-y-auto">
          <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>{text.replace(/<!--[\s\S]*?-->/g, '')}</ReactMarkdown>
        </div>
      ) : (
        <pre className="bg-gray-50 border border-gray-100 rounded-lg px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">{text}</pre>
      )}
    </div>
  )
}

function LiveLogSection({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const logRef = useRef<HTMLPreElement>(null)
  const [status, setStatus] = useState('connecting…')

  useEffect(() => {
    const es = new EventSource(`/api/v1/jobs/${jobId}/stream`)
    es.addEventListener('token', (e) => {
      const data = JSON.parse((e as MessageEvent).data)
      if (logRef.current) {
        logRef.current.textContent = (logRef.current.textContent ?? '') + data.text
        logRef.current.scrollTop = logRef.current.scrollHeight
      }
      setStatus('streaming…')
    })
    es.addEventListener('done', () => {
      es.close()
      setStatus('done')
      setTimeout(onDone, 800)
    })
    es.onerror = () => {
      es.close()
      setStatus('reconnecting…')
      setTimeout(onDone, 2000)
    }
    return () => es.close()
  }, [jobId, onDone])

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Live output</div>
        <span className="text-xs text-gray-400">{status}</span>
      </div>
      <pre ref={logRef}
        className="bg-gray-950 text-gray-100 rounded-lg p-3 text-xs min-h-24 max-h-96 overflow-y-auto whitespace-pre-wrap font-mono" />
    </div>
  )
}

function ReviewSection({ job, run, doc, onRefresh }: {
  job: JobDetail
  run: Run
  doc: DocumentDetail
  onRefresh: () => void
}) {
  const [answers, setAnswers] = useState<Record<number, string>>({})
  const [mutError, setMutError] = useState<string | null>(null)

  const approveMut = useMutation({
    mutationFn: () => api.putJobStatus(job.id, 'done'),
    onSuccess: () => { setMutError(null); onRefresh() },
    onError: (e: unknown) => setMutError(e instanceof Error ? e.message : String(e)),
  })
  const rejectMut = useMutation({
    mutationFn: () => api.putJobStatus(job.id, 'pending'),
    onSuccess: () => { setMutError(null); onRefresh() },
    onError: (e: unknown) => setMutError(e instanceof Error ? e.message : String(e)),
  })
  const clarifyMut = useMutation({
    mutationFn: async () => {
      if (run.id && run.questions?.length) {
        const updatedQuestions = run.questions.map((q, i) => ({
          ...q,
          answer: answers[i] ?? q.answer ?? null,
        }))
        await api.patchRun(job.id, run.id, { questions: updatedQuestions })
      }
      await api.putJobStatus(job.id, 'pending')
    },
    onSuccess: () => { setMutError(null); onRefresh() },
    onError: (e: unknown) => setMutError(e instanceof Error ? e.message : String(e)),
  })

  const busy = approveMut.isPending || rejectMut.isPending || clarifyMut.isPending

  const confidenceColor = run.confidence === 'high'
    ? 'bg-green-100 text-green-700'
    : run.confidence === 'medium'
    ? 'bg-blue-100 text-blue-700'
    : 'bg-red-100 text-red-700'

  return (
    <div className="space-y-4">
      <div className="bg-white rounded-xl border border-gray-200 p-4">
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Review — {job.stage}</div>
          <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${confidenceColor}`}>{run.confidence} confidence</span>
          <span className="text-xs text-gray-400">run {(job.runs?.length ?? 1)} of {job.runs?.length ?? 1}</span>
        </div>

        {/* Outputs */}
        {run.outputs?.length > 0 && (
          <div className="mb-4">
            {run.outputs.map((out, i) => (
              <div key={i} className="mb-3">
                {out.field && <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-1">{out.field}</div>}
                <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap max-h-80 overflow-y-auto">{out.text}</pre>
              </div>
            ))}
          </div>
        )}

        {/* Clarification questions */}
        {run.questions?.length > 0 && (
          <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 mb-4">
            <div className="text-xs font-semibold text-amber-800 mb-2">Clarifications needed</div>
            {run.questions.map((q, i) => (
              <div key={i} className="mb-3">
                <div className="text-xs text-gray-700 mb-1">
                  <code className="bg-white border border-gray-200 px-1 py-0.5 rounded text-xs">{q.segment}</code>
                  <span className="ml-1 text-gray-500">— {q.question}</span>
                </div>
                <input
                  value={answers[i] ?? q.answer ?? ''}
                  onChange={e => setAnswers(a => ({ ...a, [i]: e.target.value }))}
                  placeholder="Your answer…"
                  className="w-full text-sm border border-gray-200 rounded-lg px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-200"
                />
              </div>
            ))}
          </div>
        )}

        {mutError && (
          <div className="mb-3 text-xs text-red-600 bg-red-50 border border-red-100 rounded-lg px-3 py-2">{mutError}</div>
        )}
        <div className="flex gap-2">
          <button onClick={() => approveMut.mutate()} disabled={busy}
            className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            Approve
          </button>
          {run.questions?.length > 0 && (
            <button onClick={() => clarifyMut.mutate()} disabled={busy}
              className="px-4 py-1.5 text-sm font-medium bg-amber-500 text-white rounded-lg hover:bg-amber-600 disabled:opacity-50">
              Re-run with answers
            </button>
          )}
          <button onClick={() => rejectMut.mutate()} disabled={busy}
            className="px-4 py-1.5 text-sm font-medium border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:opacity-50">
            Re-run
          </button>
        </div>
      </div>

      {/* Context suggestions from LLM */}
      {run.suggestions?.additional_context?.trim() && (
        <ContextSuggestionSection
          label="Additional context suggestion"
          current={doc.additional_context}
          proposed={run.suggestions.additional_context.trim()}
          onSave={(text) => api.updateDocument(doc.id, { additional_context: text })}
          onRefresh={onRefresh}
        />
      )}
      {run.suggestions?.linked_context?.trim() && run.suggestions.linked_context_id && (
        <LinkedContextSuggestion
          contextId={run.suggestions.linked_context_id}
          proposed={run.suggestions.linked_context.trim()}
          onRefresh={onRefresh}
        />
      )}
    </div>
  )
}

function ContextSuggestionSection({ label, current, proposed, onSave, onRefresh }: {
  label: string
  current?: string | null
  proposed: string
  onSave: (text: string) => Promise<unknown>
  onRefresh: () => void
}) {
  const [edited, setEdited] = useState(proposed)
  const [dismissed, setDismissed] = useState(false)

  const saveMut = useMutation({
    mutationFn: () => onSave(edited),
    onSuccess: onRefresh,
  })

  if (dismissed) return null

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="flex items-center justify-between mb-4">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">{label}</div>
        <div className="flex gap-2">
          <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}
            className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            {saveMut.isSuccess ? 'Saved' : 'Accept'}
          </button>
          <button onClick={() => setDismissed(true)}
            className="px-4 py-1.5 text-sm font-medium border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50">
            Dismiss
          </button>
        </div>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Current</div>
          <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap h-48 overflow-y-auto">{current || '(empty)'}</pre>
        </div>
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Proposed — editable</div>
          <textarea value={edited} onChange={e => setEdited(e.target.value)}
            className="w-full bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono h-48 resize-none focus:outline-none focus:ring-2 focus:ring-blue-200" />
        </div>
      </div>
    </div>
  )
}

function LinkedContextSuggestion({ contextId, proposed, onRefresh }: {
  contextId: string
  proposed: string
  onRefresh: () => void
}) {
  const { data: contextsPage } = useQuery({
    queryKey: ['contexts'],
    queryFn: () => api.contexts(),
    staleTime: 30_000,
  })
  const entry = contextsPage?.data?.find(e => e.id === contextId)

  return (
    <ContextSuggestionSection
      label={entry ? `Linked context suggestion — "${entry.name}"` : 'Linked context suggestion'}
      current={entry?.text}
      proposed={proposed}
      onSave={(text) => api.updateContext(contextId, { text })}
      onRefresh={onRefresh}
    />
  )
}

function ErrorSection({ job, onRefresh }: { job: JobDetail; onRefresh: () => void }) {
  const latestRun = job.runs && job.runs.length > 0 ? job.runs[job.runs.length - 1] : null
  const retryMut = useMutation({
    mutationFn: () => api.putJobStatus(job.id, 'pending'),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white rounded-xl border border-red-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-red-500 uppercase tracking-wide">Error — {job.stage}</div>
        <button onClick={() => retryMut.mutate()} disabled={retryMut.isPending}
          className="text-xs text-gray-500 hover:text-green-600 border border-gray-200 hover:border-green-400 rounded-lg px-3 py-1 transition-colors disabled:opacity-50">
          Retry
        </button>
      </div>
      {latestRun && latestRun.outputs?.length > 0 && (
        <pre className="bg-red-50 border border-red-100 rounded-lg px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-48 overflow-y-auto text-red-700">
          {latestRun.outputs.map(o => o.text).join('\n')}
        </pre>
      )}
    </div>
  )
}

function EmbedImageSection({ job, onRefresh }: { job: JobDetail; onRefresh: () => void }) {
  const embedImage = job.options?.embed?.embed_image ?? false
  const mut = useMutation({
    mutationFn: () => api.updateJob(job.id, { options: { embed: { embed_image: !embedImage } } }),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-3">Image embedding</div>
      <div className="flex items-center gap-3">
        <button
          onClick={() => mut.mutate()}
          disabled={mut.isPending}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 ${
            embedImage ? 'bg-blue-600' : 'bg-gray-200'
          }`}
        >
          <span className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${
            embedImage ? 'translate-x-4' : 'translate-x-1'
          }`} />
        </button>
        <span className="text-sm text-gray-600">
          {embedImage ? 'Embed image for visual search' : 'Text only (no image embedding)'}
        </span>
      </div>
    </div>
  )
}

function JobsSection({ jobs, currentJobId, onRefresh }: {
  jobs: { id: string; stage: string; status: string; updated_at: string }[]
  currentJobId?: string
  onRefresh: () => void
}) {
  const replayMut = useMutation({
    mutationFn: (jobId: string) => api.putJobStatus(jobId, 'pending'),
    onSuccess: onRefresh,
  })

  const doneJobs = jobs.filter(j => j.status === 'done')
  if (doneJobs.length === 0) return null

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-3">Replay from stage</div>
      <div className="flex flex-wrap gap-2">
        {doneJobs.map(j => (
          <button key={j.id}
            onClick={() => {
              if (confirm(`Replay from ${j.stage}? This will reset this and all downstream stages.`))
                replayMut.mutate(j.id)
            }}
            className={`px-3 py-1.5 text-xs font-mono font-medium border rounded-lg hover:bg-gray-50 transition-colors ${
              j.id === currentJobId ? 'border-blue-300 text-blue-700' : 'border-gray-200 text-gray-700'
            }`}>
            {j.stage}
          </button>
        ))}
      </div>
    </div>
  )
}
