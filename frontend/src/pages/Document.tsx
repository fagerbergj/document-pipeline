import { useState, useEffect, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import DocKebabMenu from '../components/DocKebabMenu'
import type { DocumentDetail, JobDetail, ClarificationRequest, JobEventRecord } from '../types'

export default function Document() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const navigate = useNavigate()

  const { data: doc, isLoading: docLoading } = useQuery({
    queryKey: ['document', id],
    queryFn: () => api.document(id!),
  })

  const { data: job, isLoading: jobLoading } = useQuery({
    queryKey: ['job', id],
    queryFn: () => api.job(id!),
  })

  const { data: eventsPage } = useQuery({
    queryKey: ['job-events', id],
    queryFn: () => api.jobEvents(id!),
    enabled: !!id,
  })

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['document', id] })
    qc.invalidateQueries({ queryKey: ['job', id] })
    qc.invalidateQueries({ queryKey: ['job-events', id] })
  }

  const handleDelete = () => navigate('/')

  const isLoading = docLoading || jobLoading

  if (isLoading) return (
    <div className="flex items-center justify-center h-full py-24">
      <LoadingSpinner />
    </div>
  )
  if (!doc || !job) return (
    <div className="flex items-center justify-center h-full py-24 text-gray-400">
      Document not found
    </div>
  )

  const errorEvents = (eventsPage?.data ?? []).filter((e: JobEventRecord) => e.event_type === 'failed')

  return (
    <div>
      {/* Header bar */}
      <div className="sticky top-0 z-10 flex items-center gap-3 px-6 py-4 border-b border-gray-200 bg-white">
        <Link to="/" className="text-gray-400 hover:text-gray-600 text-sm">←</Link>
        <h1 className="text-base font-semibold text-gray-900 flex-1 truncate">{doc.title || '(untitled)'}</h1>
        <span className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-0.5 rounded">{job.current_stage}</span>
        <StatusBadge state={job.stage_state} />
        <DocKebabMenu
          docId={doc.id}
          state={job.stage_state}
          replayStages={job.replay_stages}
          onDelete={handleDelete}
          onSuccess={refresh}
        />
      </div>

      {/* Content */}
      <div className="p-6 space-y-4">
        <TitleSection doc={doc} onRefresh={refresh} />
        <ContextSection doc={doc} job={job} onRefresh={refresh} />
        {job.stage_state === 'running' && <LiveLogSection docId={doc.id} onDone={refresh} />}
        {job.review && <ReviewSection doc={doc} review={job.review} onRefresh={refresh} />}
        {(doc.has_image || doc.stage_displays.length > 0) && (
          <ArtifactsSection doc={doc} />
        )}
        {errorEvents.length > 0 && <EventLogSection events={errorEvents} docId={doc.id} onCleared={refresh} />}
        {job.replay_stages.length > 0 && <ReplaySection docId={doc.id} job={job} onRefresh={refresh} />}
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

function ContextSection({ doc, job, onRefresh }: { doc: DocumentDetail; job: JobDetail; onRefresh: () => void }) {
  const [editing, setEditing] = useState(false)
  const [ctx, setCtx] = useState(doc.document_context ?? '')
  const [contextRef, setContextRef] = useState<string>(doc.context_ref ?? '')
  const [entries, setEntries] = useState<{ id: string; name: string; text: string }[]>([])

  const required = job.context_required
  const hasContext = !!(doc.context_ref || doc.document_context)

  useEffect(() => {
    if (editing || required) {
      api.contexts().then(p => setEntries(p.data ?? [])).catch(() => {})
    }
  }, [editing, required])

  function openEdit() {
    setCtx(doc.document_context ?? '')
    setContextRef(doc.context_ref ?? '')
    setEditing(true)
  }

  const linkedEntry = entries.find(e => e.id === (editing ? contextRef : doc.context_ref))

  const saveMut = useMutation({
    mutationFn: () => api.updateDocument(doc.id, {
      document_context: ctx || null,
      context_ref: contextRef || null,
    }),
    onSuccess: () => { onRefresh(); setEditing(false) },
  })
  const runMut = useMutation({
    mutationFn: () => api.postJobEvent(doc.id, {
      type: 'provide_context',
      document_context: ctx || undefined,
      context_ref: contextRef || null,
    }),
    onSuccess: () => { onRefresh(); setEditing(false) },
  })

  // Collapsed pill when context is set and not editing
  if (hasContext && !editing) {
    const parts: string[] = []
    if (doc.context_ref) {
      const name = entries.find(e => e.id === doc.context_ref)?.name ?? `ref:${doc.context_ref.slice(0, 8)}…`
      parts.push(`↗ ${name}`)
    }
    if (doc.document_context) {
      const preview = doc.document_context.split('\n')[0].slice(0, 48)
      parts.push(preview + (doc.document_context.length > 48 || doc.document_context.includes('\n') ? '…' : ''))
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

  const canSave = !!(contextRef || ctx.trim())

  return (
    <div className={`bg-white rounded-xl border p-4 ${required ? 'border-red-300 ring-1 ring-red-200' : 'border-gray-200'}`}>
      <div className="flex items-center justify-between mb-3">
        <div className={`text-xs font-semibold uppercase tracking-wide ${required ? 'text-red-600' : 'text-gray-400'}`}>
          Document context
          {required && <span className="ml-1 font-normal normal-case text-red-500">— required to continue</span>}
        </div>
        {hasContext && (
          <button onClick={() => setEditing(false)} className="text-xs text-gray-400 hover:text-gray-600">Cancel</button>
        )}
      </div>

      {/* Saved context ref picker */}
      {entries.length > 0 && (
        <div className="mb-3">
          <label className="block text-xs text-gray-500 mb-1">Saved context</label>
          <div className="flex items-center gap-2">
            <select
              value={contextRef}
              onChange={e => setContextRef(e.target.value)}
              className="flex-1 text-sm border border-gray-200 rounded-lg px-2 py-1.5 bg-white text-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-200"
            >
              <option value="">None</option>
              {entries.map(e => <option key={e.id} value={e.id}>{e.name}</option>)}
            </select>
            {contextRef && (
              <button onClick={() => setContextRef('')} className="text-xs text-gray-400 hover:text-red-500">✕</button>
            )}
          </div>
          {linkedEntry && (
            <pre className="mt-1.5 text-xs text-gray-500 font-mono whitespace-pre-wrap line-clamp-2 px-1">{linkedEntry.text}</pre>
          )}
        </div>
      )}

      {/* Free-text context */}
      <div>
        <label className="block text-xs text-gray-500 mb-1">Additional context</label>
        <textarea value={ctx} onChange={e => setCtx(e.target.value)} rows={4}
          className={`w-full text-sm font-mono border rounded-lg px-3 py-2 resize-y focus:outline-none focus:ring-2 ${required && !contextRef ? 'border-red-300 bg-red-50 focus:ring-red-200' : 'border-gray-200 focus:ring-blue-200'}`}
          placeholder="Describe this document — used by clarify, classify, and other stages…" />
      </div>

      <div className="flex gap-2 mt-3">
        <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending || !canSave}
          className="px-3 py-1.5 text-xs font-medium border border-gray-300 rounded-lg hover:bg-gray-50 disabled:opacity-50">
          Save
        </button>
        {required && (
          <button onClick={() => runMut.mutate()} disabled={runMut.isPending || !canSave}
            className="px-3 py-1.5 text-xs font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            Save &amp; run now
          </button>
        )}
      </div>
    </div>
  )
}


function ArtifactsSection({ doc }: { doc: DocumentDetail }) {
  const tabs = [
    ...(doc.has_image ? [{ id: 'image', label: 'Image' }] : []),
    ...doc.stage_displays.map(sd => ({ id: sd.name, label: sd.name })),
  ]
  const [active, setActive] = useState(tabs[0]?.id ?? 'image')
  const activeDisplay = doc.stage_displays.find(sd => sd.name === active)

  if (tabs.length === 0) return null

  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
      <div className="flex border-b border-gray-100 overflow-x-auto">
        {tabs.map(tab => (
          <button key={tab.id} onClick={() => setActive(tab.id)}
            className={`px-4 py-2.5 text-xs font-medium whitespace-nowrap transition-colors border-b-2 -mb-px ${
              active === tab.id
                ? 'border-gray-900 text-gray-900'
                : 'border-transparent text-gray-400 hover:text-gray-600'
            }`}>
            {tab.label}
          </button>
        ))}
      </div>
      <div className="p-4">
        {active === 'image' && doc.has_image && (
          <div>
            <div className="flex justify-end mb-2">
              <a href={`/api/v1/documents/${doc.id}/image`} target="_blank" rel="noreferrer"
                className="text-xs text-gray-400 hover:text-gray-600">Open in new tab ↗</a>
            </div>
            <img src={`/api/v1/documents/${doc.id}/image`} alt="Original document"
              className="max-w-full rounded-lg border border-gray-100" />
          </div>
        )}
        {activeDisplay && (
          <ArtifactFields fields={activeDisplay.fields} />
        )}
      </div>
    </div>
  )
}

function ArtifactFields({ fields }: { fields: Record<string, string> }) {
  const [raw, setRaw] = useState(false)
  const isMarkdown = (v: string) => v.includes('\n') && /[#*`\-]/.test(v)

  return (
    <div className="space-y-3">
      {Object.entries(fields).map(([field, value]) => (
        <div key={field}>
          <div className="flex items-center justify-between mb-1">
            {Object.keys(fields).length > 1
              ? <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">{field}</div>
              : <div />}
            <div className="flex items-center gap-3">
              {isMarkdown(value) && (
                <button onClick={() => setRaw(r => !r)} className="text-xs text-gray-400 hover:text-gray-600">
                  {raw ? 'Rendered' : 'Raw'}
                </button>
              )}
              <button onClick={() => {
                const blob = new Blob([value], { type: 'text/plain' })
                window.open(URL.createObjectURL(blob), '_blank')
              }} className="text-xs text-gray-400 hover:text-gray-600">Open in new tab ↗</button>
            </div>
          </div>
          {isMarkdown(value) && !raw ? (
            <div className="prose prose-sm prose-gray max-w-none bg-gray-50 border border-gray-100 rounded-lg px-4 py-3 max-h-96 overflow-y-auto">
              <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>{value.replace(/<!--[\s\S]*?-->/g, '')}</ReactMarkdown>
            </div>
          ) : (
            <pre className="bg-gray-50 border border-gray-100 rounded-lg px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">{value}</pre>
          )}
        </div>
      ))}
    </div>
  )
}


function LiveLogSection({ docId, onDone }: { docId: string; onDone: () => void }) {
  const logRef = useRef<HTMLPreElement>(null)
  const [status, setStatus] = useState('connecting…')

  useEffect(() => {
    const es = new EventSource(`/api/v1/documents/${docId}/jobs/stream`)
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
  }, [docId, onDone])

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

function EventLogSection({ events, docId, onCleared }: { events: JobEventRecord[]; docId: string; onCleared: () => void }) {
  const clearMut = useMutation({
    mutationFn: () => api.postJobEvent(docId, { type: 'clear_errors' }),
    onSuccess: onCleared,
  })

  return (
    <div className="bg-white rounded-xl border border-red-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-red-500 uppercase tracking-wide">Error log</div>
        <button onClick={() => clearMut.mutate()} disabled={clearMut.isPending}
          className="text-xs text-gray-400 hover:text-red-500 transition-colors disabled:opacity-50">
          Clear
        </button>
      </div>
      <div className="space-y-2">
        {events.map((e, i) => (
          <div key={i} className="bg-red-50 border border-red-100 rounded-lg p-3 text-xs font-mono">
            <div className="text-gray-400 mb-1">{e.timestamp.slice(0, 19).replace('T', ' ')} · {e.stage}</div>
            <div className="text-red-700 whitespace-pre-wrap">{(e.data as { error?: string } | null)?.error ?? '(no detail)'}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

function ReviewSection({ doc, review, onRefresh }: {
  doc: DocumentDetail
  review: NonNullable<JobDetail['review']>
  onRefresh: () => void
}) {
  const [editedText, setEditedText] = useState(review.output_text)
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [freePrompt, setFreePrompt] = useState('')

  const approveMut = useMutation({
    mutationFn: () => api.postJobEvent(doc.id, {
      type: 'approve',
      edited_text: review.is_single_output ? editedText : undefined,
    }),
    onSuccess: onRefresh,
  })
  const rejectMut = useMutation({
    mutationFn: () => api.postJobEvent(doc.id, { type: 'reject' }),
    onSuccess: onRefresh,
  })
  const clarifyMut = useMutation({
    mutationFn: () => api.postJobEvent(doc.id, {
      type: 'clarify',
      answers,
      free_prompt: freePrompt,
      edited_text: review.is_single_output ? editedText : undefined,
    }),
    onSuccess: onRefresh,
  })

  const confidenceColor = review.confidence === 'high'
    ? 'bg-green-100 text-green-700'
    : review.confidence === 'medium'
    ? 'bg-blue-100 text-blue-700'
    : 'bg-red-100 text-red-700'

  const busy = approveMut.isPending || rejectMut.isPending || clarifyMut.isPending

  return (
    <div className="space-y-4">
      <div className="bg-white rounded-xl border border-gray-200 p-4">
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Review</div>
          {review.confidence && (
            <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${confidenceColor}`}>{review.confidence} confidence</span>
          )}
          {review.qa_rounds > 0 && (
            <span className="text-xs text-gray-400">{review.qa_rounds} Q&A round{review.qa_rounds !== 1 ? 's' : ''}</span>
          )}
        </div>

        {review.is_single_output ? (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3 mb-4">
            <div>
              <div className="text-xs font-semibold text-gray-400 mb-1">Before ({review.input_field})</div>
              <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap h-80 overflow-y-auto">{review.input_text}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-gray-400 mb-1">After ({review.output_field})</div>
              <textarea value={editedText} onChange={e => setEditedText(e.target.value)}
                className="w-full bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono h-80 resize-none focus:outline-none focus:ring-2 focus:ring-blue-200" />
            </div>
          </div>
        ) : (
          <div className="mb-4">
            <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap max-h-80 overflow-y-auto">{review.output_text}</pre>
          </div>
        )}

        {review.clarification_requests.length > 0 && (
          <ClarificationForm requests={review.clarification_requests} answers={answers} onChange={setAnswers} />
        )}

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 mb-1">Additional instructions</label>
          <textarea value={freePrompt} onChange={e => setFreePrompt(e.target.value)} rows={2}
            placeholder="e.g. 'focus on the meeting action items…'"
            className="w-full text-sm border border-gray-200 rounded-lg px-3 py-2 resize-y focus:outline-none focus:ring-2 focus:ring-blue-200" />
        </div>

        <div className="flex gap-2">
          <button onClick={() => approveMut.mutate()} disabled={busy}
            className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            Approve
          </button>
          <button onClick={() => clarifyMut.mutate()} disabled={busy}
            className="px-4 py-1.5 text-sm font-medium bg-amber-500 text-white rounded-lg hover:bg-amber-600 disabled:opacity-50">
            Re-run
          </button>
          <button onClick={() => rejectMut.mutate()} disabled={busy}
            className="px-4 py-1.5 text-sm font-medium border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:opacity-50">
            Reject
          </button>
        </div>
      </div>

      {review.context_updates?.trim() && (
        <ContextUpdatesSection doc={doc} proposed={review.context_updates.trim()} onRefresh={onRefresh} />
      )}
    </div>
  )
}

function ContextUpdatesSection({ doc, proposed, onRefresh }: { doc: DocumentDetail; proposed: string; onRefresh: () => void }) {
  const current = doc.document_context
  const [edited, setEdited] = useState(proposed)

  const saveMut = useMutation({
    mutationFn: () => api.updateDocument(doc.id, { document_context: edited }),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="flex items-center justify-between mb-4">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Review — Context Updates</div>
        <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}
          className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
          {saveMut.isSuccess ? 'Saved' : 'Accept'}
        </button>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Current</div>
          <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap h-72 overflow-y-auto">{current || '(empty)'}</pre>
        </div>
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Proposed — editable</div>
          <textarea value={edited} onChange={e => setEdited(e.target.value)}
            className="w-full bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono h-72 resize-none focus:outline-none focus:ring-2 focus:ring-blue-200" />
        </div>
      </div>
    </div>
  )
}

function ClarificationForm({
  requests, answers, onChange,
}: {
  requests: ClarificationRequest[]
  answers: Record<string, string>
  onChange: (a: Record<string, string>) => void
}) {
  return (
    <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 mb-3">
      <div className="text-xs font-semibold text-amber-800 mb-2">Clarifications needed</div>
      {requests.map((req, i) => (
        <div key={i} className="mb-3">
          <div className="text-xs text-gray-700 mb-1">
            <code className="bg-white border border-gray-200 px-1 py-0.5 rounded text-xs">{req.segment}</code>
            <span className="ml-1 text-gray-500">— {req.question}</span>
          </div>
          <input value={answers[String(i)] ?? ''} onChange={e => onChange({ ...answers, [i]: e.target.value })}
            placeholder="Your answer…"
            className="w-full text-sm border border-gray-200 rounded-lg px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-200" />
        </div>
      ))}
    </div>
  )
}

function ReplaySection({ docId, job, onRefresh }: { docId: string; job: JobDetail; onRefresh: () => void }) {
  const replayMut = useMutation({
    mutationFn: (stage: string) => api.postJobEvent(docId, { type: 'replay', stage }),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-3">Replay from stage</div>
      <div className="flex flex-wrap gap-2">
        {job.replay_stages.map(s => (
          <button key={s.name}
            onClick={() => {
              if (confirm(`Replay from ${s.name}? This will clear downstream stage data.`))
                replayMut.mutate(s.name)
            }}
            className="px-3 py-1.5 text-xs font-mono font-medium border border-gray-200 rounded-lg hover:bg-gray-50">
            {s.name}
          </button>
        ))}
      </div>
    </div>
  )
}
