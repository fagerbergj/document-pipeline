import { useState, useEffect, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import type { DocumentDetail, ClarificationRequest, StageEvent } from '../types'

export default function Document() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const navigate = useNavigate()

  const { data: doc, isLoading } = useQuery({
    queryKey: ['document', id],
    queryFn: () => api.document(id!),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['document', id] })

  const deleteMut = useMutation({
    mutationFn: () => api.deleteDocument(id!),
    onSuccess: () => navigate('/'),
  })

  if (isLoading) return (
    <div className="flex items-center justify-center h-full py-24">
      <LoadingSpinner />
    </div>
  )
  if (!doc) return (
    <div className="flex items-center justify-center h-full py-24 text-gray-400">
      Document not found
    </div>
  )

  const errorEvents = doc.events.filter(e => e.event_type === 'failed')

  return (
    <div>
      {/* Header bar */}
      <div className="sticky top-0 z-10 flex items-center gap-3 px-6 py-4 border-b border-gray-200 bg-white">
        <Link to="/" className="text-gray-400 hover:text-gray-600 text-sm">←</Link>
        <h1 className="text-base font-semibold text-gray-900 flex-1 truncate">{doc.title || '(untitled)'}</h1>
        <span className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-0.5 rounded">{doc.current_stage}</span>
        <StatusBadge state={doc.stage_state} />
        <KebabMenu
          state={doc.stage_state}
          onStop={() => api.stop(doc.id).then(refresh)}
          onRetry={() => api.retry(doc.id).then(refresh)}
          onDelete={() => { if (confirm('Delete this document? This cannot be undone.')) deleteMut.mutate() }}
        />
      </div>

      {/* Content */}
      <div className="p-6 space-y-4">
        <TitleSection doc={doc} onRefresh={refresh} />
        <ContextSection doc={doc} onRefresh={refresh} />
        {doc.stage_state === 'running' && <LiveLogSection docId={doc.id} onDone={refresh} />}
        {doc.review && <ReviewSection doc={doc} review={doc.review} onRefresh={refresh} />}
        {(doc.has_image || doc.stage_displays.length > 0) && (
          <ArtifactsSection doc={doc} />
        )}
        {errorEvents.length > 0 && <EventLogSection events={errorEvents} docId={doc.id} onCleared={refresh} />}
        {doc.replay_stages.length > 0 && <ReplaySection doc={doc} onRefresh={refresh} />}
      </div>
    </div>
  )
}

function TitleSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [title, setTitle] = useState(doc.title ?? '')
  const mut = useMutation({ mutationFn: (t: string) => api.updateTitle(doc.id, t), onSuccess: onRefresh })

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
  const [ctx, setCtx] = useState(doc.document_context)
  const [entries, setEntries] = useState<{ name: string; text: string }[]>([])
  const [editing, setEditing] = useState(!doc.document_context)
  const saveMut = useMutation({ mutationFn: (c: string) => api.saveContext(doc.id, c), onSuccess: onRefresh })
  const setMut = useMutation({ mutationFn: (c: string) => api.setContext(doc.id, c), onSuccess: onRefresh })

  useEffect(() => {
    api.contextLibrary().then(setEntries).catch(() => {})
  }, [])

  const required = doc.context_required

  return (
    <div className={`bg-white rounded-xl border p-4 ${required ? 'border-red-300 ring-1 ring-red-200' : 'border-gray-200'}`}>
      <div className="flex items-center justify-between mb-3">
        <div className={`text-xs font-semibold uppercase tracking-wide ${required ? 'text-red-600' : 'text-gray-400'}`}>
          Document context
          {required && <span className="ml-1 font-normal normal-case text-red-500">— required to continue</span>}
        </div>
        <div className="flex items-center gap-2">
          {ctx && (
            <button onClick={() => setEditing(e => !e)} className="text-xs text-gray-400 hover:text-gray-600">
              {editing ? 'Preview' : 'Edit'}
            </button>
          )}
          {entries.length > 0 && editing && (
            <select onChange={e => { if (e.target.value) setCtx(e.target.value) }}
              className="text-xs border border-gray-200 rounded px-2 py-1 bg-white text-gray-600">
              <option value="">Load saved…</option>
              {entries.map(e => <option key={e.name} value={e.text}>{e.name}</option>)}
            </select>
          )}
        </div>
      </div>
      {editing || !ctx ? (
        <textarea value={ctx} onChange={e => setCtx(e.target.value)} rows={4}
          className={`w-full text-sm font-mono border rounded-lg px-3 py-2 resize-y mb-3 focus:outline-none focus:ring-2 ${required ? 'border-red-300 bg-red-50 focus:ring-red-200' : 'border-gray-200 focus:ring-blue-200'}`}
          placeholder="Describe this document — used by clarify, classify, and other stages that require context…" />
      ) : (
        <div className="prose prose-sm prose-gray max-w-none bg-gray-50 border border-gray-100 rounded-lg px-4 py-3 max-h-96 overflow-y-auto mb-3 cursor-text" onClick={() => setEditing(true)}>
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{ctx}</ReactMarkdown>
        </div>
      )}
      <div className="flex gap-2">
        <button onClick={() => saveMut.mutate(ctx)} disabled={saveMut.isPending}
          className="px-3 py-1.5 text-xs font-medium border border-gray-300 rounded-lg hover:bg-gray-50 disabled:opacity-50">
          Save
        </button>
        {required && (
          <button onClick={() => setMut.mutate(ctx)} disabled={setMut.isPending}
            className="px-3 py-1.5 text-xs font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            Save &amp; run now
          </button>
        )}
      </div>
    </div>
  )
}

function KebabMenu({ state, onStop, onRetry, onDelete }: {
  state: string
  onStop: () => void
  onRetry: () => void
  onDelete: () => void
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function handler(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  return (
    <div ref={ref} className="relative">
      <button onClick={() => setOpen(o => !o)}
        className="w-8 h-8 flex items-center justify-center rounded-lg text-gray-400 hover:text-gray-600 hover:bg-gray-100 transition-colors text-lg leading-none">
        ⋯
      </button>
      {open && (
        <div className="absolute right-0 top-10 w-44 bg-white border border-gray-200 rounded-xl shadow-lg z-20 overflow-hidden">
          {state === 'running' && (
            <button onClick={() => { setOpen(false); onStop() }}
              className="w-full text-left px-4 py-2.5 text-sm text-amber-700 hover:bg-amber-50">
              Stop
            </button>
          )}
          {state === 'error' && (
            <button onClick={() => { setOpen(false); onRetry() }}
              className="w-full text-left px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-50">
              Retry
            </button>
          )}
          <button onClick={() => { setOpen(false); onDelete() }}
            className="w-full text-left px-4 py-2.5 text-sm text-red-600 hover:bg-red-50">
            Delete
          </button>
        </div>
      )}
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
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{value.replace(/<!--[\s\S]*?-->/g, '')}</ReactMarkdown>
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
    const es = new EventSource(`/api/v1/documents/${docId}/stream`)
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

function EventLogSection({ events, docId, onCleared }: { events: StageEvent[]; docId: string; onCleared: () => void }) {
  const handleClear = async () => {
    await fetch(`/api/v1/documents/${docId}/errors`, { method: 'DELETE' })
    onCleared()
  }

  return (
    <div className="bg-white rounded-xl border border-red-200 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-red-500 uppercase tracking-wide">Error log</div>
        <button onClick={handleClear} className="text-xs text-gray-400 hover:text-red-500 transition-colors">Clear</button>
      </div>
      <div className="space-y-2">
        {events.map((e, i) => (
          <div key={i} className="bg-red-50 border border-red-100 rounded-lg p-3 text-xs font-mono">
            <div className="text-gray-400 mb-1">{e.timestamp.slice(0, 19).replace('T', ' ')} · {e.stage}</div>
            <div className="text-red-700 whitespace-pre-wrap">{e.data?.error ?? '(no detail)'}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

function ReviewSection({ doc, review, onRefresh }: { doc: DocumentDetail; review: NonNullable<DocumentDetail['review']>; onRefresh: () => void }) {
  const [editedText, setEditedText] = useState(review.output_text)
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [freePrompt, setFreePrompt] = useState('')

  const approveMut = useMutation({
    mutationFn: () => api.approve(doc.id, review.is_single_output ? editedText : undefined),
    onSuccess: onRefresh
  })
  const rejectMut = useMutation({ mutationFn: () => api.reject(doc.id), onSuccess: onRefresh })
  const clarifyMut = useMutation({
    mutationFn: () => api.clarify(doc.id, answers, freePrompt),
    onSuccess: onRefresh
  })

  const confidenceColor = review.confidence === 'high'
    ? 'bg-green-100 text-green-700'
    : review.confidence === 'medium'
    ? 'bg-blue-100 text-blue-700'
    : 'bg-red-100 text-red-700'

  return (
    <div className="space-y-4">
      {/* Review — Clarifications */}
      <div className="bg-white rounded-xl border border-gray-200 p-4">
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Review — Clarifications</div>
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
              <div className="text-xs font-semibold text-gray-400 mb-1">After ({review.output_field}) — editable</div>
              <textarea value={editedText} onChange={e => setEditedText(e.target.value)}
                className="w-full bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono h-80 resize-none focus:outline-none focus:ring-2 focus:ring-blue-200" />
            </div>
          </div>
        ) : (
          <div className="mb-4">
            <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap max-h-80 overflow-y-auto">{review.output_text}</pre>
          </div>
        )}

        <div className="flex gap-2 mb-4">
          <button onClick={() => approveMut.mutate()} disabled={approveMut.isPending}
            className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
            Approve
          </button>
          <button onClick={() => rejectMut.mutate()} disabled={rejectMut.isPending}
            className="px-4 py-1.5 text-sm font-medium border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:opacity-50">
            Reject
          </button>
        </div>

        <div className="border-t border-gray-100 pt-4">
          {review.clarification_requests.length > 0 && (
            <ClarificationForm requests={review.clarification_requests} answers={answers} onChange={setAnswers} />
          )}
          <div className="mb-3">
            <label className="block text-xs font-medium text-gray-500 mb-1">Additional instructions</label>
            <textarea value={freePrompt} onChange={e => setFreePrompt(e.target.value)} rows={2}
              placeholder="e.g. 'focus on the meeting action items…'"
              className="w-full text-sm border border-gray-200 rounded-lg px-3 py-2 resize-y focus:outline-none focus:ring-2 focus:ring-blue-200" />
          </div>
          <button onClick={() => clarifyMut.mutate()} disabled={clarifyMut.isPending}
            className="px-4 py-1.5 text-sm font-medium bg-amber-500 text-white rounded-lg hover:bg-amber-600 disabled:opacity-50">
            Re-run with instructions
          </button>
        </div>
      </div>

      {/* Review — Context Updates */}
      {review.context_updates && (
        <ContextUpdatesSection proposed={review.context_updates} onRefresh={onRefresh} />
      )}
    </div>
  )
}

function ContextUpdatesSection({ proposed, onRefresh }: { proposed: string; onRefresh: () => void }) {
  const qc = useQueryClient()
  const { data: entries } = useQuery({ queryKey: ['context-library'], queryFn: api.contextLibrary })
  const current = entries?.find(e => e.name === 'user_context')?.text ?? ''
  const saveMut = useMutation({
    mutationFn: () => api.saveContextEntry('user_context', proposed),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['context-library'] }); onRefresh() },
  })

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-4">
        <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Review — Context Updates</div>
        <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}
          className="px-4 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50">
          Apply
        </button>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Current</div>
          <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap h-80 overflow-y-auto">{current || '(empty)'}</pre>
        </div>
        <div>
          <div className="text-xs font-semibold text-gray-400 mb-1">Proposed</div>
          <pre className="bg-gray-50 border border-gray-100 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap h-80 overflow-y-auto">{proposed}</pre>
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

function ReplaySection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  return (
    <div className="bg-white rounded-xl border border-gray-200 p-4">
      <div className="text-xs font-semibold text-gray-400 uppercase tracking-wide mb-3">Replay from stage</div>
      <div className="flex flex-wrap gap-2">
        {doc.replay_stages.map(s => (
          <button key={s.name}
            onClick={() => {
              if (confirm(`Replay from ${s.name}? This will clear downstream stage data.`))
                api.replay(doc.id, s.name).then(onRefresh)
            }}
            className="px-3 py-1.5 text-xs font-mono font-medium border border-gray-200 rounded-lg hover:bg-gray-50">
            {s.name}
          </button>
        ))}
      </div>
    </div>
  )
}
