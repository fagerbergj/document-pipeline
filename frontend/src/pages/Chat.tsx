import { useState, useRef, useEffect, useCallback } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { api, type ChatSessionSummary, type SourceDoc } from '../api'

interface Message {
  role: 'user' | 'assistant'
  content: string
  sources?: SourceDoc[]
  sourcesOpen?: boolean
}

function relativeDate(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  if (days < 7) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}

export default function Chat() {
  const { sessionId: urlSessionId } = useParams<{ sessionId?: string }>()
  const navigate = useNavigate()

  const [sessions, setSessions] = useState<ChatSessionSummary[]>([])
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [context, setContext] = useState('')
  const [topK, setTopK] = useState(5)
  const [showSettings, setShowSettings] = useState(false)
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState<number | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const { data: contextsPage } = useQuery({
    queryKey: ['contexts'],
    queryFn: () => api.contexts(),
  })
  const contextLibrary = contextsPage?.data ?? []

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const loadSessions = useCallback(async () => {
    const result = await api.listChatSessions()
    setSessions(result.data)
    return result.data
  }, [])

  useEffect(() => {
    loadSessions().then(data => {
      if (urlSessionId) {
        setActiveSessionId(urlSessionId)
      } else if (data.length > 0) {
        setActiveSessionId(data[0].id)
        navigate(`/chat/${data[0].id}`, { replace: true })
      }
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!activeSessionId) {
      setMessages([])
      return
    }
    api.getChatSession(activeSessionId).then(detail => {
      const sess = sessions.find(s => s.id === activeSessionId)
      setContext(detail.context)
      setTopK(detail.top_k)
      setMessages(
        detail.messages.map(m => ({
          role: m.role as 'user' | 'assistant',
          content: m.content,
          sources: m.sources ?? undefined,
          sourcesOpen: false,
        }))
      )
      if (!sess) {
        setSessions(prev => {
          const exists = prev.find(s => s.id === activeSessionId)
          if (exists) return prev
          return [{ ...detail, message_count: detail.messages.length }, ...prev]
        })
      }
    }).catch(() => {})
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSessionId])

  function activateSession(id: string) {
    if (streaming) abortRef.current?.abort()
    setActiveSessionId(id)
    navigate(`/chat/${id}`)
    setError('')
  }

  async function handleNewChat() {
    const session = await api.createChatSession({ context: context.trim(), top_k: topK })
    setSessions(prev => [session, ...prev])
    setActiveSessionId(session.id)
    setMessages([])
    navigate(`/chat/${session.id}`)
    setError('')
  }

  async function handleDeleteSession(id: string, e: React.MouseEvent) {
    e.stopPropagation()
    await api.deleteChatSession(id)
    setSessions(prev => prev.filter(s => s.id !== id))
    if (activeSessionId === id) {
      const remaining = sessions.filter(s => s.id !== id)
      if (remaining.length > 0) {
        setActiveSessionId(remaining[0].id)
        navigate(`/chat/${remaining[0].id}`)
      } else {
        setActiveSessionId(null)
        navigate('/chat')
        setMessages([])
      }
    }
  }

  function scheduleSettingsPatch(newContext: string, newTopK: number) {
    if (!activeSessionId) return
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      api.patchChatSession(activeSessionId, { context: newContext, top_k: newTopK })
        .then(updated => {
          setSessions(prev => prev.map(s => s.id === updated.id ? updated : s))
        })
        .catch(() => {})
    }, 800)
  }

  function handleContextChange(val: string) {
    setContext(val)
    scheduleSettingsPatch(val, topK)
  }

  function handleTopKChange(val: number) {
    setTopK(val)
    scheduleSettingsPatch(context, val)
  }

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
    setStreaming(false)
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = input.trim()
    if (!trimmed || streaming) return
    if (!activeSessionId) return

    const userMessage: Message = { role: 'user', content: trimmed }
    const baseMessages = [...messages, userMessage]
    setMessages(baseMessages)
    setInput('')
    setError('')
    setStreaming(true)

    const assistantIdx = baseMessages.length
    setMessages(prev => [...prev, { role: 'assistant', content: '', sources: [], sourcesOpen: false }])

    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort

    try {
      const res = await api.sendMessage(activeSessionId, trimmed, abort.signal)

      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error((data as { error?: string }).error || `${res.status} ${res.statusText}`)
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

      await loadSessions().then(data => {
        setSessions(data)
      })
    } catch (err: unknown) {
      if ((err as Error)?.name !== 'AbortError') {
        setError((err as Error)?.message || 'Request failed')
        setMessages(prev => {
          const last = prev[prev.length - 1]
          return last?.role === 'assistant' && !last.content ? prev.slice(0, -1) : prev
        })
      }
    } finally {
      setStreaming(false)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit(e as unknown as React.FormEvent)
    }
  }

  return (
    <div className="flex h-screen">
      {/* Sidebar */}
      <div className="w-[250px] flex-shrink-0 flex flex-col border-r border-gray-200 bg-white">
        <div className="p-3 border-b border-gray-200">
          <button
            onClick={handleNewChat}
            className="w-full text-sm px-3 py-2 rounded-lg bg-blue-600 text-white hover:bg-blue-700 transition-colors font-medium"
          >
            New Chat
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {sessions.length === 0 && (
            <div className="text-xs text-gray-400 text-center py-6 px-3">No conversations yet</div>
          )}
          {sessions.map(s => (
            <div
              key={s.id}
              onClick={() => activateSession(s.id)}
              className={`group relative flex flex-col px-3 py-2.5 cursor-pointer border-b border-gray-100 hover:bg-gray-50 transition-colors ${activeSessionId === s.id ? 'bg-blue-50' : ''}`}
            >
              <span className={`text-sm truncate pr-6 ${activeSessionId === s.id ? 'text-blue-700 font-medium' : 'text-gray-800'}`}>
                {s.title || 'New chat'}
              </span>
              <span className="text-xs text-gray-400 mt-0.5">{relativeDate(s.updated_at)}</span>
              <button
                onClick={e => handleDeleteSession(s.id, e)}
                className="absolute right-2 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 text-gray-400 hover:text-red-500 transition-opacity p-1 rounded"
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>

      {/* Main chat area */}
      <div className="flex flex-col flex-1 min-w-0">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-3 border-b border-gray-200 bg-white">
          <h1 className="text-base font-semibold text-gray-900">
            {activeSessionId
              ? (sessions.find(s => s.id === activeSessionId)?.title || 'New chat')
              : 'Chat'}
          </h1>
          <button
            onClick={() => setShowSettings(s => !s)}
            className={`text-xs px-3 py-1.5 rounded border transition-colors ${showSettings ? 'bg-gray-100 border-gray-300 text-gray-700' : 'border-gray-200 text-gray-500 hover:text-gray-700 hover:border-gray-300'}`}
          >
            Settings
          </button>
        </div>

        {/* Settings panel */}
        {showSettings && (
          <div className="px-6 py-4 border-b border-gray-200 bg-gray-50 space-y-3">
            <div className="flex items-start gap-6">
              <div className="flex-1">
                <div className="flex items-center justify-between mb-1">
                  <label className="text-xs font-medium text-gray-600">Context <span className="text-gray-400">(optional)</span></label>
                  {contextLibrary.length > 0 && (
                    <select
                      className="text-xs text-gray-500 border border-gray-200 rounded px-2 py-0.5 focus:outline-none focus:ring-1 focus:ring-blue-400"
                      defaultValue=""
                      onChange={e => {
                        const entry = contextLibrary.find(x => x.name === e.target.value)
                        if (entry) handleContextChange(entry.text)
                      }}
                    >
                      <option value="">Load saved…</option>
                      {contextLibrary.map(e => (
                        <option key={e.id} value={e.name}>{e.name}</option>
                      ))}
                    </select>
                  )}
                </div>
                <textarea
                  className="w-full rounded border border-gray-300 px-2 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                  rows={3}
                  placeholder="Add context to guide answers…"
                  value={context}
                  onChange={e => handleContextChange(e.target.value)}
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Sources</label>
                <input
                  type="number"
                  min={1}
                  max={20}
                  value={topK}
                  onChange={e => handleTopKChange(Number(e.target.value))}
                  className="w-14 rounded border border-gray-300 px-2 py-1.5 text-xs text-center focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            </div>
          </div>
        )}

        {/* Messages */}
        <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6">
          {!activeSessionId && (
            <div className="text-center text-gray-400 text-sm mt-20">
              Start a new chat or select a conversation
            </div>
          )}
          {activeSessionId && messages.length === 0 && !streaming && (
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
                          {streaming && idx === messages.length - 1 && (
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

                    {msg.content && (!streaming || idx < messages.length - 1) && (
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
              className="flex-1 rounded-xl border border-gray-300 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none disabled:opacity-50"
              rows={1}
              placeholder={activeSessionId ? 'Ask something… (Enter to send, Shift+Enter for newline)' : 'Select or start a chat first'}
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              disabled={streaming || !activeSessionId}
            />
            {streaming ? (
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
                disabled={!input.trim() || !activeSessionId}
                className="px-4 py-3 rounded-xl bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors whitespace-nowrap"
              >
                Send
              </button>
            )}
          </form>
        </div>
      </div>
    </div>
  )
}
