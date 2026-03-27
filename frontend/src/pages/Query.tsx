import { useState, useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { api } from '../api'
import type { ContextEntry } from '../types'

interface SourceDoc {
  doc_id: string
  title: string
  summary: string
  date_month: string
  score: number
}

interface Message {
  role: 'user' | 'assistant'
  content: string
  sources?: SourceDoc[]
  sourcesOpen?: boolean
}

export default function Query() {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [context, setContext] = useState('')
  const [topK, setTopK] = useState(5)
  const [showSettings, setShowSettings] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState<number | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const { data: contextLibrary } = useQuery<ContextEntry[]>({
    queryKey: ['context-library'],
    queryFn: api.contextLibrary,
  })

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  function toggleSources(idx: number) {
    setMessages(prev => prev.map((m, i) =>
      i === idx ? { ...m, sourcesOpen: !m.sourcesOpen } : m
    ))
  }

  function handleCopy(idx: number, content: string) {
    navigator.clipboard.writeText(content)
    setCopied(idx)
    setTimeout(() => setCopied(null), 2000)
  }

  function handleDownload(content: string, idx: number) {
    const blob = new Blob([content], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `answer-${idx + 1}.md`
    a.click()
    URL.revokeObjectURL(url)
  }

  function handleStop() {
    abortRef.current?.abort()
    setLoading(false)
  }

  function clearConversation() {
    abortRef.current?.abort()
    setMessages([])
    setError('')
    setLoading(false)
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = input.trim()
    if (!trimmed || loading) return

    const userMessage: Message = { role: 'user', content: trimmed }
    const updatedMessages = [...messages, userMessage]
    setMessages(updatedMessages)
    setInput('')
    setError('')
    setLoading(true)

    // placeholder for streaming assistant reply
    const assistantIdx = updatedMessages.length
    setMessages(prev => [...prev, { role: 'assistant', content: '', sources: [], sourcesOpen: false }])

    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort

    try {
      const res = await fetch('/api/v1/query', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          messages: updatedMessages.map(m => ({ role: m.role, content: m.content })),
          context: context.trim(),
          top_k: topK,
        }),
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
          if (!line.startsWith('data: ')) continue
          const raw = line.slice(6).trim()
          if (!raw || raw === '{}') continue
          try {
            const parsed = JSON.parse(raw)
            if (Array.isArray(parsed)) {
              setMessages(prev => prev.map((m, i) =>
                i === assistantIdx ? { ...m, sources: parsed } : m
              ))
            } else if (parsed.text !== undefined) {
              setMessages(prev => prev.map((m, i) =>
                i === assistantIdx ? { ...m, content: m.content + parsed.text } : m
              ))
            } else if (parsed.error) {
              setError(parsed.error)
            }
          } catch {
            // ignore
          }
        }
      }
    } catch (err: unknown) {
      if ((err as Error)?.name !== 'AbortError') {
        setError((err as Error)?.message || 'Request failed')
        // remove empty placeholder on error
        setMessages(prev => {
          const last = prev[prev.length - 1]
          return last?.role === 'assistant' && !last.content ? prev.slice(0, -1) : prev
        })
      }
    } finally {
      setLoading(false)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit(e as unknown as React.FormEvent)
    }
  }

  return (
    <div className="flex flex-col h-screen">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-3 border-b border-gray-200 bg-white">
        <h1 className="text-base font-semibold text-gray-900">Query Knowledge Base</h1>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowSettings(s => !s)}
            className={`text-xs px-3 py-1.5 rounded border transition-colors ${showSettings ? 'bg-gray-100 border-gray-300 text-gray-700' : 'border-gray-200 text-gray-500 hover:text-gray-700 hover:border-gray-300'}`}
          >
            Settings
          </button>
          {messages.length > 0 && (
            <button
              onClick={clearConversation}
              className="text-xs px-3 py-1.5 rounded border border-gray-200 text-gray-500 hover:text-gray-700 hover:border-gray-300 transition-colors"
            >
              New conversation
            </button>
          )}
        </div>
      </div>

      {/* Settings panel */}
      {showSettings && (
        <div className="px-6 py-4 border-b border-gray-200 bg-gray-50 space-y-3">
          <div className="flex items-start gap-6">
            {/* Context */}
            <div className="flex-1">
              <div className="flex items-center justify-between mb-1">
                <label className="text-xs font-medium text-gray-600">Context <span className="text-gray-400">(optional)</span></label>
                {contextLibrary && contextLibrary.length > 0 && (
                  <select
                    className="text-xs text-gray-500 border border-gray-200 rounded px-2 py-0.5 focus:outline-none focus:ring-1 focus:ring-blue-400"
                    defaultValue=""
                    onChange={e => {
                      const entry = contextLibrary.find(x => x.name === e.target.value)
                      if (entry) setContext(entry.text)
                    }}
                  >
                    <option value="">Load saved…</option>
                    {contextLibrary.map(e => (
                      <option key={e.name} value={e.name}>{e.name}</option>
                    ))}
                  </select>
                )}
              </div>
              <textarea
                className="w-full rounded border border-gray-300 px-2 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                rows={3}
                placeholder="Add context to guide answers…"
                value={context}
                onChange={e => setContext(e.target.value)}
              />
            </div>
            {/* Top-K */}
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Sources</label>
              <input
                type="number"
                min={1}
                max={20}
                value={topK}
                onChange={e => setTopK(Number(e.target.value))}
                className="w-14 rounded border border-gray-300 px-2 py-1.5 text-xs text-center focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>
        </div>
      )}

      {/* Messages */}
      <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6">
        {messages.length === 0 && (
          <div className="text-center text-gray-400 text-sm mt-20">
            Ask a question about your notes
          </div>
        )}

        {messages.map((msg, idx) => (
          <div key={idx} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            <div className={`max-w-2xl w-full ${msg.role === 'user' ? 'ml-12' : 'mr-12'}`}>

              {msg.role === 'user' ? (
                <div className="bg-blue-600 text-white rounded-2xl rounded-tr-sm px-4 py-3 text-sm whitespace-pre-wrap">
                  {msg.content}
                </div>
              ) : (
                <div>
                  <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-5 py-4">
                    {msg.content ? (
                      <div className="prose prose-sm max-w-none">
                        <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>
                          {msg.content}
                        </ReactMarkdown>
                        {loading && idx === messages.length - 1 && (
                          <span className="inline-block w-1.5 h-4 bg-gray-400 animate-pulse ml-0.5 align-middle" />
                        )}
                      </div>
                    ) : (
                      <span className="flex items-center gap-1 h-5">
                        <span className="w-2 h-2 rounded-full bg-gray-400 animate-bounce [animation-delay:-0.3s]" />
                        <span className="w-2 h-2 rounded-full bg-gray-400 animate-bounce [animation-delay:-0.15s]" />
                        <span className="w-2 h-2 rounded-full bg-gray-400 animate-bounce" />
                      </span>
                    )}
                  </div>

                  {/* Assistant message actions */}
                  {msg.content && (!loading || idx < messages.length - 1) && (
                    <div className="flex items-center gap-3 mt-1.5 px-1">
                      <button
                        onClick={() => handleCopy(idx, msg.content)}
                        className="text-xs text-gray-400 hover:text-gray-600 transition-colors"
                      >
                        {copied === idx ? 'Copied!' : 'Copy'}
                      </button>
                      <button
                        onClick={() => handleDownload(msg.content, idx)}
                        className="text-xs text-gray-400 hover:text-gray-600 transition-colors"
                      >
                        Download
                      </button>
                      {msg.sources && msg.sources.length > 0 && (
                        <button
                          onClick={() => toggleSources(idx)}
                          className="text-xs text-gray-400 hover:text-gray-600 transition-colors ml-auto"
                        >
                          {msg.sourcesOpen ? '▾' : '▸'} {msg.sources.length} source{msg.sources.length !== 1 ? 's' : ''}
                        </button>
                      )}
                    </div>
                  )}

                  {/* Sources */}
                  {msg.sourcesOpen && msg.sources && msg.sources.length > 0 && (
                    <div className="mt-2 space-y-1.5">
                      {msg.sources.map((s, si) => (
                        <Link
                          key={si}
                          to={`/documents/${s.doc_id}`}
                          className="block rounded-lg border border-gray-200 bg-white px-4 py-2.5 hover:border-blue-300 hover:bg-blue-50 transition-colors"
                        >
                          <div className="flex items-start justify-between gap-2">
                            <span className="text-sm font-medium text-gray-800">{s.title}</span>
                            <span className="text-xs text-gray-400 whitespace-nowrap">score {s.score.toFixed(3)}</span>
                          </div>
                          {s.date_month && <div className="text-xs text-gray-400 mt-0.5">{s.date_month}</div>}
                          {s.summary && <div className="text-xs text-gray-600 mt-1 line-clamp-2">{s.summary}</div>}
                        </Link>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        ))}

        {error && (
          <div className="rounded-md bg-red-50 border border-red-200 px-4 py-3 text-sm text-red-700">
            {error}
          </div>
        )}

        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div className="border-t border-gray-200 bg-white px-6 py-4">
        <form onSubmit={handleSubmit} className="flex gap-3 items-end">
          <textarea
            ref={textareaRef}
            className="flex-1 rounded-xl border border-gray-300 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
            rows={1}
            placeholder="Ask something… (Enter to send, Shift+Enter for newline)"
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            disabled={loading}
          />
          {loading ? (
            <button
              type="button"
              onClick={handleStop}
              className="px-4 py-3 rounded-xl bg-gray-200 text-gray-700 text-sm font-medium hover:bg-gray-300 transition-colors whitespace-nowrap"
            >
              Stop
            </button>
          ) : (
            <button
              type="submit"
              disabled={!input.trim()}
              className="px-4 py-3 rounded-xl bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors whitespace-nowrap"
            >
              Send
            </button>
          )}
        </form>
      </div>
    </div>
  )
}
