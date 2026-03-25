import { useState, useEffect, useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import type { DocumentDetail, ClarificationRequest } from '../types'

export default function Document() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()

  const { data: doc, isLoading } = useQuery({
    queryKey: ['document', id],
    queryFn: () => api.document(id!),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['document', id] })

  if (isLoading) return <LoadingSpinner />
  if (!doc) return <div className="text-gray-500 py-12 text-center">Document not found</div>

  return (
    <div className="space-y-4 max-w-4xl">
      <Link to="/" className="text-sm text-gray-500 hover:text-gray-700">← Dashboard</Link>
      <HeaderSection doc={doc} onRefresh={refresh} />
      <ContextSection doc={doc} onRefresh={refresh} />
      {doc.stage_displays.map(sd => (
        <StageResultSection key={sd.name} name={sd.name} fields={sd.fields} />
      ))}
      {doc.stage_state === 'running' && <LiveLogSection docId={doc.id} onDone={refresh} />}
      {doc.review && <ReviewSection doc={doc} review={doc.review} onRefresh={refresh} />}
      {doc.replay_stages.length > 0 && <ReplaySection doc={doc} onRefresh={refresh} />}
    </div>
  )
}

function HeaderSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [title, setTitle] = useState(doc.title ?? '')
  const mut = useMutation({ mutationFn: (t: string) => api.updateTitle(doc.id, t), onSuccess: onRefresh })

  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <div className="flex flex-wrap items-center gap-3 mb-3">
        <h1 className="text-xl font-bold text-gray-800 flex-1">{doc.title || '(untitled)'}</h1>
        <code className="text-sm text-gray-600 bg-gray-100 px-2 py-0.5 rounded">{doc.current_stage}</code>
        <StatusBadge state={doc.stage_state} />
        {doc.stage_state === 'running' && (
          <button onClick={() => api.stop(doc.id).then(onRefresh)}
            className="px-3 py-1 text-sm bg-yellow-50 text-yellow-800 border border-yellow-400 rounded hover:bg-yellow-100">Stop</button>
        )}
        {doc.stage_state === 'error' && (
          <button onClick={() => api.retry(doc.id).then(onRefresh)}
            className="px-3 py-1 text-sm bg-red-50 text-red-800 border border-red-400 rounded hover:bg-red-100">Retry</button>
        )}
      </div>
      <div className="text-xs text-gray-400 mb-3">Received {doc.created_at.slice(0,19).replace('T',' ')}</div>
      <form onSubmit={e => { e.preventDefault(); mut.mutate(title) }} className="flex gap-2">
        <input value={title} onChange={e => setTitle(e.target.value)}
          className="flex-1 text-sm border border-gray-300 rounded px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-gray-300" />
        <button type="submit" className="px-3 py-1.5 text-sm bg-gray-100 border border-gray-300 rounded hover:bg-gray-200">Save</button>
      </form>
    </div>
  )
}

function ContextSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [ctx, setCtx] = useState(doc.document_context)
  const [entries, setEntries] = useState<{ name: string; text: string }[]>([])
  const saveMut = useMutation({ mutationFn: (c: string) => api.saveContext(doc.id, c), onSuccess: onRefresh })
  const setMut = useMutation({ mutationFn: (c: string) => api.setContext(doc.id, c), onSuccess: onRefresh })

  useEffect(() => {
    api.contextLibrary().then(setEntries).catch(() => {})
  }, [])

  const required = doc.context_required

  return (
    <div className={`bg-white rounded-lg shadow-sm p-4 ${required ? 'border-2 border-red-400' : ''}`}>
      <h2 className={`text-sm font-semibold uppercase tracking-wide mb-3 ${required ? 'text-red-600' : 'text-gray-500'}`}>
        Document context {required && <span className="font-normal normal-case">— required to continue</span>}
      </h2>
      {entries.length > 0 && (
        <select onChange={e => { if (e.target.value) setCtx(e.target.value) }}
          className="text-sm border border-gray-300 rounded px-2 py-1.5 mb-2 bg-white w-full sm:w-auto">
          <option value="">— load saved —</option>
          {entries.map(e => <option key={e.name} value={e.text}>{e.name}</option>)}
        </select>
      )}
      <textarea value={ctx} onChange={e => setCtx(e.target.value)} rows={4}
        className={`w-full text-sm font-mono border rounded px-3 py-2 resize-y mb-2 focus:outline-none focus:ring-2 focus:ring-gray-300 ${required ? 'border-red-400 bg-red-50' : 'border-gray-300'}`}
        placeholder="Describe this document — used by clarify, classify, and other stages that require context…" />
      <div className="flex gap-2">
        <button onClick={() => saveMut.mutate(ctx)}
          className="px-3 py-1.5 text-sm bg-gray-100 border border-gray-300 rounded hover:bg-gray-200">Save context</button>
        {required && (
          <button onClick={() => setMut.mutate(ctx)}
            className="px-3 py-1.5 text-sm bg-green-100 text-green-800 border border-green-400 rounded hover:bg-green-200">Save &amp; run now</button>
        )}
      </div>
    </div>
  )
}

function StageResultSection({ name, fields }: { name: string; fields: Record<string, string> }) {
  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 mb-3">{name}</h2>
      {Object.entries(fields).map(([field, value]) => (
        <div key={field} className="mb-3">
          <div className="text-xs font-semibold uppercase tracking-wide text-gray-400 mb-1">{field}</div>
          <pre className="bg-gray-50 border border-gray-200 rounded px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">{value}</pre>
        </div>
      ))}
    </div>
  )
}

function LiveLogSection({ docId, onDone }: { docId: string; onDone: () => void }) {
  const logRef = useRef<HTMLPreElement>(null)
  const [status, setStatus] = useState('connecting…')

  useEffect(() => {
    const es = new EventSource(`/api/documents/${docId}/stream`)
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
      setStatus('done — reloading…')
      setTimeout(onDone, 800)
    })
    es.onerror = () => {
      es.close()
      setStatus('stream ended')
      setTimeout(onDone, 2000)
    }
    return () => es.close()
  }, [docId, onDone])

  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 mb-3">
        Live output <span className="font-normal normal-case text-gray-400 ml-1">{status}</span>
      </h2>
      <pre ref={logRef} className="bg-gray-900 text-gray-100 rounded p-3 text-xs min-h-20 max-h-96 overflow-y-auto whitespace-pre-wrap" />
    </div>
  )
}

function ReviewSection({ doc, review, onRefresh }: { doc: DocumentDetail; review: NonNullable<DocumentDetail['review']>; onRefresh: () => void }) {
  const [editedText, setEditedText] = useState(review.output_text)
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [freePrompt, setFreePrompt] = useState('')
  const [activeTab, setActiveTab] = useState<'side' | 'diff'>('side')

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
    ? 'bg-green-100 text-green-800'
    : review.confidence === 'medium'
    ? 'bg-cyan-100 text-cyan-800'
    : 'bg-red-100 text-red-800'

  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Review — {review.stage_name}</h2>
        {review.confidence && (
          <span className={`px-2 py-0.5 rounded text-xs font-semibold ${confidenceColor}`}>{review.confidence} confidence</span>
        )}
        {review.qa_rounds > 0 && (
          <span className="text-xs text-gray-400">{review.qa_rounds} Q&A round{review.qa_rounds !== 1 ? 's' : ''}</span>
        )}
      </div>

      {review.is_single_output ? (
        <>
          <div className="flex gap-1 mb-3">
            {(['side', 'diff'] as const).map(tab => (
              <button key={tab} onClick={() => setActiveTab(tab)}
                className={`px-3 py-1 text-sm rounded border ${activeTab === tab ? 'bg-gray-700 text-white border-gray-700' : 'bg-gray-50 text-gray-600 border-gray-300 hover:bg-gray-100'}`}>
                {tab === 'side' ? 'Side by side' : 'Diff'}
              </button>
            ))}
          </div>
          {activeTab === 'side' ? (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3 mb-3">
              <div>
                <div className="text-xs font-semibold text-gray-400 mb-1">Before ({review.input_field})</div>
                <pre className="bg-gray-50 border border-gray-200 rounded p-2 text-xs font-mono whitespace-pre-wrap h-96 overflow-y-auto">{review.input_text}</pre>
              </div>
              <div>
                <div className="text-xs font-semibold text-gray-400 mb-1">After ({review.output_field}) — editable</div>
                <textarea value={editedText} onChange={e => setEditedText(e.target.value)}
                  className="w-full bg-gray-50 border border-gray-200 rounded p-2 text-xs font-mono h-96 resize-none focus:outline-none focus:ring-2 focus:ring-gray-300" />
              </div>
            </div>
          ) : (
            <DiffView before={review.input_text} after={editedText} />
          )}
        </>
      ) : (
        <div className="mb-3">
          <pre className="bg-gray-50 border border-gray-200 rounded p-2 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">{review.output_text}</pre>
        </div>
      )}

      <div className="flex gap-2 mb-4">
        <button onClick={() => approveMut.mutate()} disabled={approveMut.isPending}
          className="px-4 py-1.5 text-sm bg-green-100 text-green-800 border border-green-300 rounded hover:bg-green-200 disabled:opacity-50">Approve</button>
        <button onClick={() => rejectMut.mutate()} disabled={rejectMut.isPending}
          className="px-4 py-1.5 text-sm bg-red-100 text-red-800 border border-red-300 rounded hover:bg-red-200 disabled:opacity-50">Reject</button>
      </div>

      <div className="border-t border-gray-100 pt-4">
        {review.clarification_requests.length > 0 && (
          <ClarificationForm
            requests={review.clarification_requests}
            answers={answers}
            onChange={setAnswers}
          />
        )}
        <div className="mb-2">
          <label className="block text-xs text-gray-500 mb-1">Additional instructions (optional)</label>
          <textarea value={freePrompt} onChange={e => setFreePrompt(e.target.value)} rows={3}
            placeholder="e.g. 'focus on the meeting action items…'"
            className="w-full text-sm font-mono border border-gray-300 rounded px-3 py-2 resize-y focus:outline-none focus:ring-2 focus:ring-gray-300" />
        </div>
        <button onClick={() => clarifyMut.mutate()} disabled={clarifyMut.isPending}
          className="px-4 py-1.5 text-sm bg-yellow-50 text-yellow-800 border border-yellow-300 rounded hover:bg-yellow-100 disabled:opacity-50">
          Re-run with instructions
        </button>
      </div>
    </div>
  )
}

function ClarificationForm({
  requests,
  answers,
  onChange,
}: {
  requests: ClarificationRequest[]
  answers: Record<string, string>
  onChange: (a: Record<string, string>) => void
}) {
  return (
    <div className="border border-gray-200 rounded p-3 mb-3">
      <div className="text-sm font-semibold mb-2">Clarifications needed:</div>
      {requests.map((req, i) => (
        <div key={i} className="mb-3">
          <div className="text-sm"><code className="bg-gray-100 px-1 rounded">{req.segment}</code> — {req.question}</div>
          <input value={answers[String(i)] ?? ''} onChange={e => onChange({ ...answers, [i]: e.target.value })}
            placeholder="Your answer…"
            className="mt-1 w-full text-sm border border-gray-300 rounded px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-gray-300" />
        </div>
      ))}
    </div>
  )
}

function DiffView({ before, after }: { before: string; after: string }) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-3 mb-3">
      <pre className="bg-gray-50 border border-gray-200 rounded p-2 text-xs font-mono whitespace-pre-wrap h-96 overflow-y-auto">{before}</pre>
      <pre className="bg-gray-50 border border-gray-200 rounded p-2 text-xs font-mono whitespace-pre-wrap h-96 overflow-y-auto">{after}</pre>
    </div>
  )
}

function ReplaySection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 mb-3">Replay from stage</h2>
      <div className="flex flex-wrap gap-2">
        {doc.replay_stages.map(s => (
          <button key={s.name}
            onClick={() => {
              if (confirm(`Replay from ${s.name}? This will clear downstream stage data.`))
                api.replay(doc.id, s.name).then(onRefresh)
            }}
            className="px-3 py-1.5 text-sm bg-gray-100 border border-gray-300 rounded hover:bg-gray-200">
            {s.name}
          </button>
        ))}
      </div>
    </div>
  )
}
