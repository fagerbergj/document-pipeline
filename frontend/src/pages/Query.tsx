import { useState, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from '../api'
import type { ContextEntry } from '../types'

interface SourceDoc {
  doc_id: string
  title: string
  summary: string
  date_month: string
  score: number
}

export default function Query() {
  const [query, setQuery] = useState('')
  const [context, setContext] = useState('')
  const [topK, setTopK] = useState(5)
  const [sources, setSources] = useState<SourceDoc[]>([])
  const [answer, setAnswer] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [phase, setPhase] = useState<'idle' | 'searching' | 'answering' | 'done'>('idle')
  const abortRef = useRef<AbortController | null>(null)

  const { data: contextLibrary } = useQuery<ContextEntry[]>({
    queryKey: ['context-library'],
    queryFn: api.contextLibrary,
  })

  function loadSavedContext(name: string) {
    const entry = contextLibrary?.find(e => e.name === name)
    if (entry) setContext(entry.text)
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!query.trim() || loading) return

    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort

    setLoading(true)
    setError('')
    setAnswer('')
    setSources([])
    setPhase('searching')

    try {
      const res = await fetch('/api/v1/query', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: query.trim(), context: context.trim(), top_k: topK }),
        signal: abort.signal,
      })

      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `${res.status} ${res.statusText}`)
      }

      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let buf = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop()!

        for (const line of lines) {
          if (line.startsWith('event: sources')) {
            // next data line has the sources
          } else if (line.startsWith('event: token')) {
            setPhase('answering')
          } else if (line.startsWith('event: done')) {
            setPhase('done')
          } else if (line.startsWith('event: error')) {
            // will be parsed from data line
          } else if (line.startsWith('data: ')) {
            const raw = line.slice(6).trim()
            if (!raw || raw === '{}') continue
            try {
              const parsed = JSON.parse(raw)
              if (Array.isArray(parsed)) {
                setSources(parsed)
                setPhase('answering')
              } else if (parsed.text !== undefined) {
                setAnswer(prev => prev + parsed.text)
              } else if (parsed.error) {
                setError(parsed.error)
                setPhase('idle')
              }
            } catch {
              // ignore parse errors
            }
          }
        }
      }
      setPhase('done')
    } catch (err: unknown) {
      if ((err as Error)?.name !== 'AbortError') {
        setError((err as Error)?.message || 'Request failed')
        setPhase('idle')
      }
    } finally {
      setLoading(false)
    }
  }

  function handleStop() {
    abortRef.current?.abort()
    setLoading(false)
    setPhase('done')
  }

  return (
    <div className="max-w-3xl mx-auto px-6 py-8 space-y-6">
      <h1 className="text-xl font-semibold text-gray-900">Query Knowledge Base</h1>

      <form onSubmit={handleSubmit} className="space-y-4">
        {/* Query */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Question</label>
          <textarea
            className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
            rows={3}
            placeholder="Ask something about your notes…"
            value={query}
            onChange={e => setQuery(e.target.value)}
            disabled={loading}
          />
        </div>

        {/* Context */}
        <div>
          <div className="flex items-center justify-between mb-1">
            <label className="block text-sm font-medium text-gray-700">Context <span className="text-gray-400 font-normal">(optional)</span></label>
            {contextLibrary && contextLibrary.length > 0 && (
              <select
                className="text-xs text-gray-500 border border-gray-200 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-blue-400"
                defaultValue=""
                onChange={e => { if (e.target.value) loadSavedContext(e.target.value) }}
                disabled={loading}
              >
                <option value="">Load saved context…</option>
                {contextLibrary.map(e => (
                  <option key={e.name} value={e.name}>{e.name}</option>
                ))}
              </select>
            )}
          </div>
          <textarea
            className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
            rows={3}
            placeholder="Add context to guide the answer…"
            value={context}
            onChange={e => setContext(e.target.value)}
            disabled={loading}
          />
        </div>

        {/* Top-K */}
        <div className="flex items-center gap-3">
          <label className="text-sm font-medium text-gray-700 whitespace-nowrap">Sources to retrieve</label>
          <input
            type="number"
            min={1}
            max={20}
            value={topK}
            onChange={e => setTopK(Number(e.target.value))}
            className="w-16 rounded border border-gray-300 px-2 py-1 text-sm text-center focus:outline-none focus:ring-2 focus:ring-blue-500"
            disabled={loading}
          />
        </div>

        {/* Actions */}
        <div className="flex gap-2">
          <button
            type="submit"
            disabled={loading || !query.trim()}
            className="px-4 py-2 rounded-md bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {loading && phase === 'searching' ? 'Searching…' : loading ? 'Answering…' : 'Ask'}
          </button>
          {loading && (
            <button
              type="button"
              onClick={handleStop}
              className="px-4 py-2 rounded-md bg-gray-200 text-gray-700 text-sm font-medium hover:bg-gray-300 transition-colors"
            >
              Stop
            </button>
          )}
        </div>
      </form>

      {error && (
        <div className="rounded-md bg-red-50 border border-red-200 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {/* Sources */}
      {sources.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide mb-2">Sources</h2>
          <div className="space-y-2">
            {sources.map((s, i) => (
              <div key={i} className="rounded-md border border-gray-200 bg-white px-4 py-3">
                <div className="flex items-start justify-between gap-2">
                  <span className="text-sm font-medium text-gray-800">{s.title}</span>
                  <span className="text-xs text-gray-400 whitespace-nowrap">score {s.score.toFixed(3)}</span>
                </div>
                {s.date_month && <div className="text-xs text-gray-400 mt-0.5">{s.date_month}</div>}
                {s.summary && <div className="text-xs text-gray-600 mt-1 line-clamp-2">{s.summary}</div>}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Answer */}
      {(answer || (loading && phase === 'answering')) && (
        <div>
          <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide mb-2">Answer</h2>
          <div className="rounded-md border border-gray-200 bg-white px-5 py-4 prose prose-sm max-w-none">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{answer || ' '}</ReactMarkdown>
            {loading && phase === 'answering' && (
              <span className="inline-block w-1.5 h-4 bg-gray-400 animate-pulse ml-0.5 align-middle" />
            )}
          </div>
        </div>
      )}
    </div>
  )
}
