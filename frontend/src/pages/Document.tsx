import { useState, useEffect, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { api } from '../api'
import type { DocumentDetail, JobDetail, JobSummary, Run, Artifact } from '../types'
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

  const { data: allJobsPage } = useQuery({
    queryKey: ['jobs-for-doc', id],
    queryFn: () => api.jobs({ document_id: id!, page_size: 50 }),
    enabled: !!id,
  })
  const doneJobs = (allJobsPage?.data ?? []).filter(j => j.status === 'done')

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

  const latestRun = job?.runs && job.runs.length > 0
    ? (job.runs.slice().reverse().find(r => (r.outputs?.length ?? 0) > 0 || (r.questions?.length ?? 0) > 0)
       ?? job.runs[job.runs.length - 1])
    : null

  return (
    <div>
      {/* Header bar */}
      <div className="sticky top-0 z-10 flex items-center gap-3 px-4 py-3 sm:px-6 sm:py-4 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
        <Link to="/" className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-sm">←</Link>
        <h1 className="text-base font-semibold text-gray-900 dark:text-white flex-1 truncate">{doc.title || '(untitled)'}</h1>
        {job && (
          <>
            <span className="text-xs font-mono text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded">{job.stage}</span>
            <StatusBadge state={job.status} />
          </>
        )}
        {job && job.options?.embed !== undefined && (
          <JobOptionsMenu job={job} onRefresh={refresh} />
        )}
        <DocKebabMenu
          docId={doc.id}
          onDelete={handleDelete}
          onSuccess={refresh}
        />
      </div>

      {/* Content */}
      <div className="p-4 sm:p-6 space-y-8">
        <section className="space-y-3">
          <TitleSection doc={doc} onRefresh={refresh} />
        </section>

        <section className="space-y-3">
          <SeriesSection doc={doc} onRefresh={refresh} />
        </section>

        <section className="space-y-3">
          <h2 className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-widest">Context</h2>
          <ContextSection doc={doc} onRefresh={refresh} />
        </section>

        <section className="space-y-3">
          <h2 className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-widest">Jobs</h2>
          {job?.status === 'running' && <LiveLogSection jobId={job.id} onDone={refresh} />}
          {job?.status === 'waiting' && latestRun && (
            <ReviewSection job={job} run={latestRun} onRefresh={refresh} />
          )}
          {job?.status === 'error' && (
            <ErrorSection job={job} onRefresh={refresh} />
          )}
          {doneJobs.length > 0 && <PipelineResultsSection jobs={doneJobs} currentJobId={jobId} />}
          {!job && doneJobs.length === 0 && (
            <div className="text-xs text-gray-400 dark:text-gray-500">No jobs yet.</div>
          )}
        </section>

        {(doc.artifacts?.length ?? 0) > 0 && (
          <section className="space-y-3">
            <h2 className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-widest">Artifacts</h2>
            <ArtifactsSection doc={doc} />
          </section>
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
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-4">
      <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-3">Title</div>
      <form onSubmit={e => { e.preventDefault(); mut.mutate(title) }} className="flex gap-2">
        <input value={title} onChange={e => setTitle(e.target.value)}
          className="flex-1 text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400" />
        <button type="submit" disabled={mut.isPending}
          className="px-4 py-2 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 disabled:opacity-50">
          Save
        </button>
      </form>
      <div className="text-xs text-gray-400 dark:text-gray-500 mt-2">Received {doc.created_at.slice(0, 19).replace('T', ' ')}</div>
    </div>
  )
}

function SeriesSection({ doc, onRefresh }: { doc: DocumentDetail; onRefresh: () => void }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(doc.series ?? '')
  const [open, setOpen] = useState(false)
  const [seriesList, setSeriesList] = useState<string[]>([])
  const inputRef = useRef<HTMLInputElement>(null)
  const pickingRef = useRef(false)

  const mut = useMutation({
    mutationFn: (s: string) => api.updateDocument(doc.id, { series: s || null }),
    onSuccess: () => { onRefresh(); setEditing(false); setOpen(false) },
  })

  function startEdit() {
    setValue(doc.series ?? '')
    setEditing(true)
    api.documents({ page_size: 200 }).then(p => {
      const list = [...new Set((p.data ?? []).map((d: { series?: string | null }) => d.series).filter((s: string | null | undefined): s is string => !!s))]
      setSeriesList(list)
      setOpen(true)
    }).catch(() => {})
  }

  useEffect(() => { if (editing) inputRef.current?.focus() }, [editing])

  const save = (v: string) => { setOpen(false); mut.mutate(v) }
  const filtered = seriesList.filter(s => s.toLowerCase().includes(value.toLowerCase()) && s !== value)

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-4">
      <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-3">Series</div>
      {editing ? (
        <div className="relative">
          <div className="flex gap-2">
            <input
              ref={inputRef}
              value={value}
              onChange={e => { setValue(e.target.value); setOpen(true) }}
              onBlur={() => { if (!pickingRef.current) save(value) }}
              onKeyDown={e => {
                if (e.key === 'Enter') { e.preventDefault(); save(value) }
                if (e.key === 'Escape') { setEditing(false); setOpen(false) }
              }}
              placeholder="e.g. Colliding Worlds"
              className="flex-1 text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
              disabled={mut.isPending}
            />
            <button onClick={() => { setEditing(false); setOpen(false) }}
              className="px-3 py-2 text-sm text-gray-500 hover:text-gray-700 dark:hover:text-gray-300">
              ✕
            </button>
          </div>
          {open && filtered.length > 0 && (
            <ul className="absolute z-50 top-full left-0 mt-1 w-full bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg shadow-lg py-1 text-sm">
              {filtered.map(s => (
                <li key={s}
                  onMouseDown={e => { e.preventDefault(); pickingRef.current = true; setValue(s); save(s) }}
                  className="px-3 py-2 cursor-pointer hover:bg-gray-100 dark:hover:bg-gray-700 text-gray-800 dark:text-gray-100">
                  {s}
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : (
        <div className="flex items-center gap-2 group cursor-pointer" onClick={startEdit}>
          <span className="text-sm text-gray-700 dark:text-gray-200">
            {doc.series || <span className="text-gray-400 italic">None — click to set</span>}
          </span>
          <span className="opacity-0 group-hover:opacity-100 text-gray-400 text-xs transition-opacity">✎</span>
        </div>
      )}
      <div className="text-xs text-gray-400 dark:text-gray-500 mt-2">Documents in the same series are embedded together as a shared corpus.</div>
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
          className="inline-flex items-center gap-1 px-2.5 py-1 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-600 dark:text-gray-300 text-xs rounded-full transition-colors max-w-sm truncate">
          <span className="truncate">{parts.join(' · ')}</span>
          <span className="text-gray-400 dark:text-gray-500 flex-shrink-0">✎</span>
        </button>
      </div>
    )
  }

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">Document context</div>
        {hasContext && (
          <button onClick={() => setEditing(false)} className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300">Cancel</button>
        )}
      </div>

      {/* Linked contexts */}
      {entries.length > 0 && (
        <div className="mb-3">
          <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Linked contexts</label>
          <div className="space-y-1">
            {entries.map(e => (
              <label key={e.id} className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={linkedIds.includes(e.id)}
                  onChange={() => toggleLinked(e.id)}
                  className="rounded border-gray-300 dark:border-gray-600"
                />
                <span className="text-sm text-gray-700 dark:text-gray-200">{e.name}</span>
              </label>
            ))}
          </div>
        </div>
      )}
      {entries.length === 0 && (
        <div className="mb-3 text-xs text-gray-400 dark:text-gray-500">
          No saved contexts —{' '}
          <Link to="/contexts" className="text-blue-500 hover:underline">create one in Context Library</Link>
        </div>
      )}

      {/* Free-text additional context */}
      <div>
        <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Additional context</label>
        <textarea value={ctx} onChange={e => setCtx(e.target.value)} rows={4}
          className="w-full text-sm font-mono border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 resize-y focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          placeholder="Describe this document — used by clarify, classify, and other stages…" />
      </div>

      <div className="flex gap-2 mt-3">
        <button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}
          className="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-gray-600 rounded-lg hover:bg-gray-50 dark:hover:bg-gray-700 dark:text-gray-200 disabled:opacity-50">
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
  if (!artifacts.length) return null
  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 overflow-hidden">
      <div className="flex border-b border-gray-100 dark:border-gray-700 overflow-x-auto">
        {artifacts.map(a => (
          <button key={a.id} onClick={() => setActiveId(a.id)}
            className={`px-4 py-2.5 text-xs font-medium whitespace-nowrap transition-colors border-b-2 -mb-px ${
              activeId === a.id
                ? 'border-gray-900 dark:border-gray-100 text-gray-900 dark:text-white'
                : 'border-transparent text-gray-400 dark:text-gray-500 hover:text-gray-600 dark:hover:text-gray-300'
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

function PipelineResultsSection({ jobs, currentJobId }: { jobs: JobSummary[]; currentJobId: string | null }) {
  const sorted = [...jobs].sort((a, b) => a.updated_at.localeCompare(b.updated_at))
  const [expandedId, setExpandedId] = useState<string | null>(currentJobId)

  useEffect(() => { setExpandedId(currentJobId) }, [currentJobId])

  return (
    <div className="space-y-2">
      {sorted.map(j => (
        <StageOutputCard
          key={j.id}
          jobSummary={j}
          expanded={j.id === expandedId}
          onToggle={() => setExpandedId(id => id === j.id ? null : j.id)}
        />
      ))}
    </div>
  )
}

function StageOutputCard({ jobSummary, expanded, onToggle }: { jobSummary: JobSummary; expanded: boolean; onToggle: () => void }) {
  const { data: job } = useQuery({
    queryKey: ['job', jobSummary.id],
    queryFn: () => api.job(jobSummary.id),
    enabled: expanded,
  })
  const outputs = (job?.runs?.length ? job.runs[job.runs.length - 1].outputs : [])
    ?.filter(o => o.text?.trim()) ?? []

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 overflow-hidden">
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition-colors"
      >
        <span className="text-xs font-mono text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded">{jobSummary.stage}</span>
        <span className="text-gray-400 dark:text-gray-500 text-xs">{expanded ? '▲' : '▼'}</span>
      </button>
      {expanded && (
        <div className="px-4 pb-4 space-y-4">
          {outputs.length === 0
            ? <div className="text-xs text-gray-400 dark:text-gray-500">No text outputs for this stage.</div>
            : outputs.map((out, i) => <OutputField key={i} field={out.field ?? ''} text={out.text} />)
          }
        </div>
      )}
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
            className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300">Open in new tab ↗</a>
        </div>
        <img src={url} alt={artifact.filename}
          className="max-w-full rounded-lg border border-gray-100 dark:border-gray-700" />
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
        <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">{filename}</div>
        <div className="flex items-center gap-3">
          {isMarkdown && (
            <button onClick={onToggleRaw} className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300">
              {raw ? 'Rendered' : 'Raw'}
            </button>
          )}
          <button onClick={() => {
            const blob = new Blob([text], { type: 'text/plain' })
            window.open(URL.createObjectURL(blob), '_blank')
          }} className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300">Open in new tab ↗</button>
        </div>
      </div>
      {isMarkdown && !raw ? (
        <div className="prose prose-sm prose-gray dark:prose-invert max-w-none bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 rounded-lg px-4 py-3 max-h-96 overflow-y-auto">
          <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>{text.replace(/<!--[\s\S]*?-->/g, '')}</ReactMarkdown>
        </div>
      ) : (
        <pre className="bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 text-gray-800 dark:text-gray-200 rounded-lg px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-y-auto">{text}</pre>
      )}
    </div>
  )
}

function LiveLogSection({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const logRef = useRef<HTMLPreElement>(null)
  const [status, setStatus] = useState('connecting…')
  const [statusMsg, setStatusMsg] = useState('')
  const hasTokens = useRef(false)

  useEffect(() => {
    hasTokens.current = false
    setStatusMsg('')
    let errorCount = 0
    const es = new EventSource(`/api/v1/jobs/${jobId}/stream`)
    es.addEventListener('status', (e) => {
      if (!hasTokens.current) {
        const data = JSON.parse((e as MessageEvent).data)
        setStatusMsg(data.text ?? '')
      }
    })
    es.addEventListener('token', (e) => {
      errorCount = 0
      const data = JSON.parse((e as MessageEvent).data)
      if (logRef.current) {
        if (!hasTokens.current) {
          hasTokens.current = true
          setStatusMsg('')
          logRef.current.textContent = ''
        }
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
      errorCount++
      if (errorCount >= 3) {
        es.close()
        onDone()
      }
      // otherwise let EventSource auto-reconnect silently
    }
    return () => es.close()
  }, [jobId, onDone])

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">Live output</div>
        <span className="text-xs text-gray-400 dark:text-gray-500">{status}</span>
      </div>
      {status === 'connecting…' && (
        <p className="text-xs text-gray-500 animate-pulse mb-2">{statusMsg || 'Waiting for model…'}</p>
      )}
      <pre ref={logRef}
        className="bg-gray-950 text-gray-100 rounded-lg p-3 text-xs min-h-24 max-h-96 overflow-y-auto whitespace-pre-wrap font-mono" />
    </div>
  )
}

function ReviewSection({ job, run, onRefresh }: {
  job: JobDetail
  run: Run
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
      <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-4">
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">Review — {job.stage}</div>
          <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${confidenceColor}`}>{run.confidence} confidence</span>
          <span className="text-xs text-gray-400 dark:text-gray-500">run {(job.runs?.length ?? 1)} of {job.runs?.length ?? 1}</span>
        </div>

        {/* Outputs */}
        {run.outputs?.length > 0 && (
          <div className="mb-4">
            {run.outputs.map((out, i) => (
              <div key={i} className="mb-3">
                {out.field && <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-1">{out.field}</div>}
                <pre className="bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 text-gray-800 dark:text-gray-200 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap max-h-80 overflow-y-auto">{out.text}</pre>
              </div>
            ))}
          </div>
        )}

        {/* Clarification questions */}
        {run.questions?.length > 0 && (
          <div className="bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 rounded-lg p-3 mb-4">
            <div className="text-xs font-semibold text-amber-800 dark:text-amber-400 mb-2">Clarifications needed</div>
            {run.questions.map((q, i) => (
              <div key={i} className="mb-3">
                <div className="text-xs text-gray-700 dark:text-gray-300 mb-1">
                  <code className="bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 px-1 py-0.5 rounded text-xs">{q.segment}</code>
                  <span className="ml-1 text-gray-500 dark:text-gray-400">— {q.question}</span>
                </div>
                <input
                  value={answers[i] ?? q.answer ?? ''}
                  onChange={e => setAnswers(a => ({ ...a, [i]: e.target.value }))}
                  placeholder="Your answer…"
                  className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
                />
              </div>
            ))}
          </div>
        )}

        {mutError && (
          <div className="mb-3 text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/30 border border-red-100 dark:border-red-800 rounded-lg px-3 py-2">{mutError}</div>
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
            className="px-4 py-1.5 text-sm font-medium border border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-200 rounded-lg hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-50">
            Re-run
          </button>
        </div>
      </div>

    </div>
  )
}

function ErrorSection({ job, onRefresh }: { job: JobDetail; onRefresh: () => void }) {
  const latestRun = job.runs && job.runs.length > 0 ? job.runs[job.runs.length - 1] : null
  const retryMut = useMutation({
    mutationFn: () => api.putJobStatus(job.id, 'pending'),
    onSuccess: onRefresh,
  })

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-red-200 dark:border-red-800 p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="text-xs font-semibold text-red-500 dark:text-red-400 uppercase tracking-wide">Error — {job.stage}</div>
        <button onClick={() => retryMut.mutate()} disabled={retryMut.isPending}
          className="text-xs text-gray-500 dark:text-gray-400 hover:text-green-600 border border-gray-200 dark:border-gray-600 hover:border-green-400 rounded-lg px-3 py-1 transition-colors disabled:opacity-50">
          Retry
        </button>
      </div>
      {latestRun && latestRun.outputs?.length > 0 && (
        <pre className="bg-red-50 dark:bg-red-950/30 border border-red-100 dark:border-red-800 rounded-lg px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-48 overflow-y-auto text-red-700 dark:text-red-400">
          {latestRun.outputs.map(o => o.text).join('\n')}
        </pre>
      )}
    </div>
  )
}


function OutputField({ field, text }: { field: string; text: string }) {
  const isMarkdown = field === 'clarified_text' || field === 'summary'
  const isTags = field === 'tags'

  let tags: string[] = []
  if (isTags) {
    try { tags = JSON.parse(text) } catch { tags = [] }
  }

  return (
    <div>
      {field && (
        <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-1">{field.replace(/_/g, ' ')}</div>
      )}
      {isTags ? (
        <div className="flex flex-wrap gap-1">
          {tags.map((t, i) => (
            <span key={i} className="px-2 py-0.5 text-xs bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 border border-blue-200 dark:border-blue-800 rounded-full">{t}</span>
          ))}
        </div>
      ) : isMarkdown ? (
        <div className="prose prose-sm dark:prose-invert max-w-none text-sm text-gray-700 dark:text-gray-200">
          <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>{text}</ReactMarkdown>
        </div>
      ) : (
        <pre className="bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 text-gray-800 dark:text-gray-200 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap max-h-80 overflow-y-auto">{text}</pre>
      )}
    </div>
  )
}

function JobOptionsMenu({ job, onRefresh }: { job: JobDetail; onRefresh: () => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function handler(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const embedImage = job.options?.embed?.embed_image ?? false
  const mut = useMutation({
    mutationFn: () => api.updateJob(job.id, { options: { embed: { embed_image: !embedImage } } }),
    onSuccess: onRefresh,
  })

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(o => !o)}
        title="Job options"
        className="w-8 h-8 flex items-center justify-center rounded-lg text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors text-base"
      >
        ⚙
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-56 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-lg dark:shadow-black/40 p-3 z-20">
          <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-2">Job options</div>
          <label className="flex items-center gap-3 cursor-pointer">
            <button
              onClick={() => mut.mutate()}
              disabled={mut.isPending}
              className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
                embedImage ? 'bg-blue-600' : 'bg-gray-200 dark:bg-gray-600'
              }`}
            >
              <span className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${
                embedImage ? 'translate-x-4' : 'translate-x-1'
              }`} />
            </button>
            <span className="text-sm text-gray-700 dark:text-gray-200">Embed image</span>
          </label>
        </div>
      )}
    </div>
  )
}

