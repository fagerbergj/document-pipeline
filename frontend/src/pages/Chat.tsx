import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type ChatSummary } from '../api'
import {
  AssistantParts,
  appendTextPart,
  appendToolCall,
  appendConfirmation,
  fillToolResult,
  markConfirmation,
  partsToText,
  type MessagePart,
} from '../components/AgentParts'

interface Message {
  role: 'user' | 'assistant'
  // User messages carry plain content. Assistant messages carry an ordered
  // parts array (text segments + tool calls) reconstructed from the SSE
  // stream; their plain content is derived from the text parts on demand.
  content: string
  parts?: MessagePart[]
}

// readAgentStream parses a server-sent agent event stream into ordered tokens,
// tool calls, and tool results. It tracks the `event:` line so handlers can
// dispatch by typed event rather than guessing from payload shape.
interface ConfirmationRequestPayload {
  callId: string
  toolName: string
  hint: string
  payload: Record<string, unknown>
}

async function readAgentStream(
  body: ReadableStream<Uint8Array>,
  handlers: {
    onToken: (text: string) => void
    onToolCall: (name: string, args: Record<string, unknown>) => void
    onToolResult: (name: string, result: unknown) => void
    onConfirmationRequest: (req: ConfirmationRequestPayload) => void
    onError: (msg: string) => void
  },
): Promise<void> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let currentEvent = 'message'
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    const lines = buf.split('\n')
    buf = lines.pop()!
    for (const line of lines) {
      if (line.startsWith('event: ')) {
        currentEvent = line.slice(7).trim()
        continue
      }
      if (!line.startsWith('data: ')) continue
      const raw = line.slice(6).trim()
      if (!raw) continue
      let parsed: unknown
      try { parsed = JSON.parse(raw) } catch { continue }
      switch (currentEvent) {
        case 'token':
          if (hasStringField(parsed, 'text')) handlers.onToken(parsed.text)
          break
        case 'tool_call':
          if (hasStringField(parsed, 'name')) {
            const args = (parsed as { args?: Record<string, unknown> }).args ?? {}
            handlers.onToolCall(parsed.name, args)
          }
          break
        case 'tool_result':
          if (hasStringField(parsed, 'name')) {
            const result = (parsed as unknown as { result: unknown }).result
            handlers.onToolResult(parsed.name, result)
          }
          break
        case 'confirmation_request':
          if (hasStringField(parsed, 'call_id')) {
            const p = parsed as { call_id: string; tool_name?: string; hint?: string; payload?: Record<string, unknown> }
            handlers.onConfirmationRequest({
              callId: p.call_id,
              toolName: p.tool_name ?? '',
              hint: p.hint ?? '',
              payload: p.payload ?? {},
            })
          }
          break
        case 'error':
          if (hasStringField(parsed, 'error')) handlers.onError(parsed.error)
          break
      }
    }
  }
}

// hasStringField is a type guard for "the JSON payload has a non-empty
// string field of this name." Used to discriminate SSE event payloads.
function hasStringField<K extends string>(v: unknown, field: K): v is Record<K, string> {
  return typeof v === 'object' && v !== null && typeof (v as Record<string, unknown>)[field] === 'string'
}

// withParts updates a message's parts and keeps content in sync as the
// plain-text rendering of the text parts (for copy/download).
function withParts(m: Message, parts: MessagePart[]): Message {
  return { ...m, parts, content: partsToText(parts) }
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
  const { chatId: urlChatId } = useParams<{ chatId?: string }>()
  const navigate = useNavigate()

  const [chats, setChats] = useState<ChatSummary[]>([])
  const [activeChatId, setActiveChatId] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [maxSources, setMaxSources] = useState(5)
  const [minScore, setMinScore] = useState(0.5)
  const [showSettings, setShowSettings] = useState(false)
  const [chatListOpen, setChatListOpen] = useState(false)
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState<number | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Abort any in-flight stream on unmount so setState doesn't fire on an
  // unmounted component (matters for both the initial-message stream and the
  // confirmation-resume stream).
  useEffect(() => () => abortRef.current?.abort(), [])

  const { data: contextsPage } = useQuery({
    queryKey: ['contexts'],
    queryFn: () => api.contexts(),
  })
  const contextLibrary = contextsPage?.data ?? []

  // Scroll to bottom only when a new message is added (not on streaming token updates)
  const messageCountRef = useRef(0)
  useEffect(() => {
    if (messages.length > messageCountRef.current) {
      messageCountRef.current = messages.length
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [messages])

  const loadChats = useCallback(async () => {
    const result = await api.listChats()
    setChats(result.data)
    return result.data
  }, [])

  useEffect(() => {
    loadChats().then(data => {
      if (urlChatId) {
        setActiveChatId(urlChatId)
      } else if (data.length > 0) {
        setActiveChatId(data[0].id)
        navigate(`/chat/${data[0].id}`, { replace: true })
      }
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!activeChatId) {
      setMessages([])
      return
    }
    api.getChat(activeChatId).then(detail => {
      setSystemPrompt(detail.system_prompt ?? '')
      setMaxSources(detail.rag_retrieval?.max_sources ?? 5)
      setMinScore(detail.rag_retrieval?.minimum_score ?? 0.5)
      setMessages(
        detail.messages.map(m => ({
          role: m.role as 'user' | 'assistant',
          content: m.content,
          // Historical messages only have the persisted text; tool calls
          // from past turns aren't reconstructed.
          parts: m.role === 'assistant' ? [{ kind: 'text', text: m.content }] : undefined,
        }))
      )
      setChats(prev => {
        const exists = prev.find(s => s.id === activeChatId)
        if (exists) return prev
        return [detail, ...prev]
      })
    }).catch(() => {})
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeChatId])

  function activateChat(id: string) {
    if (streaming) abortRef.current?.abort()
    setActiveChatId(id)
    navigate(`/chat/${id}`)
    setError('')
  }

  async function handleNewChat() {
    const chat = await api.createChat({
      system_prompt: systemPrompt.trim() || undefined,
      rag_retrieval: { enabled: true, max_sources: maxSources, minimum_score: minScore },
    })
    setChats(prev => [chat, ...prev])
    setActiveChatId(chat.id)
    setMessages([])
    navigate(`/chat/${chat.id}`)
    setError('')
  }

  async function handleDeleteChat(id: string, e: React.MouseEvent) {
    e.stopPropagation()
    await api.deleteChat(id)
    setChats(prev => prev.filter(s => s.id !== id))
    if (activeChatId === id) {
      const remaining = chats.filter(s => s.id !== id)
      if (remaining.length > 0) {
        setActiveChatId(remaining[0].id)
        navigate(`/chat/${remaining[0].id}`)
      } else {
        setActiveChatId(null)
        navigate('/chat')
        setMessages([])
      }
    }
  }

  function scheduleSettingsPatch(newPrompt: string, newMaxSources: number, newMinScore: number) {
    if (!activeChatId) return
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      api.patchChat(activeChatId, {
        system_prompt: newPrompt || null,
        rag_retrieval: { enabled: true, max_sources: newMaxSources, minimum_score: newMinScore },
      })
        .then(updated => {
          setChats(prev => prev.map(s => s.id === updated.id ? updated : s))
        })
        .catch(() => {})
    }, 800)
  }

  function handlePromptChange(val: string) {
    setSystemPrompt(val)
    scheduleSettingsPatch(val, maxSources, minScore)
  }

  function handleMaxSourcesChange(val: number) {
    setMaxSources(val)
    scheduleSettingsPatch(systemPrompt, val, minScore)
  }

  function handleMinScoreChange(val: number) {
    setMinScore(val)
    scheduleSettingsPatch(systemPrompt, maxSources, val)
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

  // streamHandlersForMessage returns the SSE event handlers wired to the
  // assistant message at `idx`. Shared by the initial-message submission
  // and the approval-resume path so both flows append into the same message.
  function streamHandlersForMessage(idx: number) {
    return {
      onToken: (text: string) => {
        setMessages(prev => prev.map((m, i) =>
          i === idx ? withParts(m, appendTextPart(m.parts ?? [], text)) : m
        ))
      },
      onToolCall: (name: string, args: Record<string, unknown>) => {
        setMessages(prev => prev.map((m, i) =>
          i === idx ? withParts(m, appendToolCall(m.parts ?? [], name, args)) : m
        ))
      },
      onToolResult: (name: string, result: unknown) => {
        setMessages(prev => prev.map((m, i) =>
          i === idx ? withParts(m, fillToolResult(m.parts ?? [], name, result)) : m
        ))
      },
      onConfirmationRequest: (req: ConfirmationRequestPayload) => {
        // update_document's payload carries before/after/field/stage. Other
        // tools that ever start requesting confirmation will need their
        // own renderer; for now we assume the doc-edit shape.
        const p = req.payload as { field?: string; stage?: string; before?: string; after?: string }
        setMessages(prev => prev.map((m, i) =>
          i === idx ? withParts(m, appendConfirmation(m.parts ?? [], {
            callId: req.callId,
            toolName: req.toolName,
            hint: req.hint,
            field: p.field ?? '',
            stage: p.stage ?? '',
            before: p.before ?? '',
            after: p.after ?? '',
          })) : m
        ))
      },
      onError: (msg: string) => setError(msg),
    }
  }

  // decideConfirmation is the click handler for the inline ConfirmationBlock.
  // Approve: POSTs the decision + (possibly edited) content; reads the SSE
  // continuation into the same assistant message.
  // Reject: POSTs the rejection; marks the card status. The server returns
  // a single `done` event and does not resume the agent.
  async function decideConfirmation(callId: string, confirmed: boolean, content?: string) {
    if (!activeChatId) return
    // Resolve the hosting message index from the current snapshot (avoids a
    // side-effect-in-updater pattern that React 18 StrictMode double-invokes).
    const hostIdx = messages.findIndex(m =>
      m.role === 'assistant' && m.parts?.some(p => p.kind === 'confirmation' && p.callId === callId)
    )
    if (hostIdx < 0) return
    setMessages(prev => prev.map(m =>
      m.parts ? { ...m, parts: markConfirmation(m.parts, callId, confirmed ? 'approved' : 'rejected') } : m
    ))

    setStreaming(true)
    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort
    try {
      const res = await api.decideConfirmation(activeChatId, callId, { confirmed, content }, abort.signal)
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error((data as { error?: string }).error || `${res.status} ${res.statusText}`)
      }
      if (!confirmed) {
        // Server returns a single done event; no continuation. Drain to release the connection.
        await res.body?.cancel().catch(() => {})
        return
      }
      await readAgentStream(res.body!, streamHandlersForMessage(hostIdx))
    } catch (err: unknown) {
      if ((err as Error)?.name !== 'AbortError') {
        setError((err as Error)?.message || 'Request failed')
      }
    } finally {
      setStreaming(false)
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = input.trim()
    if (!trimmed || streaming) return
    if (!activeChatId) return

    const userMessage: Message = { role: 'user', content: trimmed }
    const baseMessages = [...messages, userMessage]
    setMessages(baseMessages)
    setInput('')
    setError('')
    setStreaming(true)

    const assistantIdx = baseMessages.length
    setMessages(prev => [...prev, { role: 'assistant', content: '', parts: [] }])

    abortRef.current?.abort()
    const abort = new AbortController()
    abortRef.current = abort

    try {
      const res = await api.sendMessage(activeChatId, trimmed, abort.signal)

      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error((data as { error?: string }).error || `${res.status} ${res.statusText}`)
      }

      await readAgentStream(res.body!, streamHandlersForMessage(assistantIdx))

      await loadChats().then(data => setChats(data))
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
      {/* Mobile backdrop for chat list */}
      {chatListOpen && (
        <div
          className="md:hidden fixed inset-0 z-30 bg-black/50"
          onClick={() => setChatListOpen(false)}
          aria-hidden="true"
        />
      )}

      {/* Chat list sidebar */}
      <div className={`
        fixed md:static inset-y-0 left-0 z-40
        w-[250px] flex-shrink-0 flex flex-col
        border-r border-gray-200 dark:border-gray-700
        bg-white dark:bg-gray-800
        transition-transform duration-200
        md:translate-x-0
        ${chatListOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}
      `}>
        <div className="p-3 border-b border-gray-200 dark:border-gray-700 flex items-center gap-2">
          <button
            onClick={handleNewChat}
            className="flex-1 text-sm px-3 py-2 rounded-lg bg-blue-600 text-white hover:bg-blue-700 transition-colors font-medium"
          >
            New Chat
          </button>
          <button
            onClick={() => setChatListOpen(false)}
            className="md:hidden text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 p-1.5 rounded transition-colors"
            aria-label="Close chat list"
          >
            ✕
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {chats.length === 0 && (
            <div className="text-xs text-gray-400 dark:text-gray-500 text-center py-6 px-3">No conversations yet</div>
          )}
          {chats.map(s => (
            <div
              key={s.id}
              onClick={() => { activateChat(s.id); setChatListOpen(false) }}
              className={`group relative flex flex-col px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors ${activeChatId === s.id ? 'bg-blue-50 dark:bg-blue-900/30' : ''}`}
            >
              <span className={`text-sm truncate pr-6 ${activeChatId === s.id ? 'text-blue-700 dark:text-blue-400 font-medium' : 'text-gray-800 dark:text-gray-100'}`}>
                {s.title || 'New chat'}
              </span>
              <span className="text-xs text-gray-400 dark:text-gray-500 mt-0.5">{relativeDate(s.updated_at)}</span>
              <button
                onClick={e => handleDeleteChat(s.id, e)}
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
        <div className="flex items-center justify-between px-4 py-3 sm:px-6 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
          <div className="flex items-center gap-2 min-w-0">
            {/* Toggle chat list — mobile only */}
            <button
              onClick={() => setChatListOpen(o => !o)}
              className="md:hidden flex-shrink-0 w-8 h-8 flex items-center justify-center rounded text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
              aria-label="Toggle chat list"
            >
              ☰
            </button>
            <h1 className="text-base font-semibold text-gray-900 dark:text-white truncate">
              {activeChatId
                ? (chats.find(s => s.id === activeChatId)?.title || 'New chat')
                : 'Chat'}
            </h1>
          </div>
          <button
            onClick={() => setShowSettings(s => !s)}
            className={`text-xs px-3 py-1.5 rounded border transition-colors flex-shrink-0 ${showSettings ? 'bg-gray-100 dark:bg-gray-700 border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-200' : 'border-gray-200 dark:border-gray-600 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 hover:border-gray-300 dark:hover:border-gray-500'}`}
          >
            Settings
          </button>
        </div>

        {/* Settings panel */}
        {showSettings && (
          <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 space-y-3">
            <div className="flex items-start gap-6">
              <div className="flex-1">
                <div className="flex items-center justify-between mb-1">
                  <label className="text-xs font-medium text-gray-600 dark:text-gray-300">System prompt <span className="text-gray-400 dark:text-gray-500">(optional)</span></label>
                  {contextLibrary.length > 0 && (
                    <select
                      className="text-xs text-gray-500 dark:text-gray-400 border border-gray-200 dark:border-gray-600 rounded px-2 py-0.5 focus:outline-none focus:ring-1 focus:ring-blue-400 dark:bg-gray-700 dark:text-gray-100"
                      defaultValue=""
                      onChange={e => {
                        const entry = contextLibrary.find(x => x.name === e.target.value)
                        if (entry) handlePromptChange(entry.text)
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
                  className="w-full rounded border border-gray-300 dark:border-gray-600 px-2 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
                  rows={3}
                  placeholder="Add context to guide answers…"
                  value={systemPrompt}
                  onChange={e => handlePromptChange(e.target.value)}
                />
              </div>
              <div className="flex gap-4">
                <div>
                  <label className="block text-xs font-medium text-gray-600 dark:text-gray-300 mb-1">Max sources</label>
                  <input
                    type="number"
                    min={1}
                    max={20}
                    value={maxSources}
                    onChange={e => handleMaxSourcesChange(Number(e.target.value))}
                    className="w-14 rounded border border-gray-300 dark:border-gray-600 px-2 py-1.5 text-xs text-center focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:text-gray-100"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-600 dark:text-gray-300 mb-1">Min score</label>
                  <input
                    type="number"
                    min={0}
                    max={1}
                    step={0.05}
                    value={minScore}
                    onChange={e => handleMinScoreChange(Number(e.target.value))}
                    className="w-16 rounded border border-gray-300 dark:border-gray-600 px-2 py-1.5 text-xs text-center focus:outline-none focus:ring-2 focus:ring-blue-500 dark:bg-gray-700 dark:text-gray-100"
                  />
                </div>
              </div>
            </div>
          </div>
        )}

        {/* Messages */}
        <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6">
          {!activeChatId && (
            <div className="text-center text-gray-400 dark:text-gray-500 text-sm mt-20">
              Start a new chat or select a conversation
            </div>
          )}
          {activeChatId && messages.length === 0 && !streaming && (
            <div className="text-center text-gray-400 dark:text-gray-500 text-sm mt-20">
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
                    <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl rounded-tl-sm px-5 py-4">
                      {msg.parts && msg.parts.length > 0 && (
                        <AssistantParts parts={msg.parts} showCursor={streaming && idx === messages.length - 1} onDecideConfirmation={decideConfirmation} />
                      )}
                      {streaming && idx === messages.length - 1 && (
                        <span className="flex items-center gap-1 h-5 mt-2">
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
                          className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors"
                        >
                          {copied === idx ? 'Copied!' : 'Copy'}
                        </button>
                        <button
                          onClick={() => handleDownload(msg.content, idx)}
                          className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors"
                        >
                          Download
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))}

          {error && (
            <div className="rounded-md bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 px-4 py-3 text-sm text-red-700 dark:text-red-400">
              {error}
            </div>
          )}

          <div ref={bottomRef} />
        </div>

        {/* Input */}
        <div className="border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 px-6 py-4">
          <form onSubmit={handleSubmit} className="flex gap-3 items-end">
            <textarea
              className="flex-1 rounded-xl border border-gray-300 dark:border-gray-600 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none disabled:opacity-50 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
              rows={1}
              placeholder={activeChatId ? 'Ask something… (Enter to send, Shift+Enter for newline)' : 'Select or start a chat first'}
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              disabled={streaming || !activeChatId}
            />
            {streaming ? (
              <button
                type="button"
                onClick={handleStop}
                className="px-4 py-3 rounded-xl bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-200 text-sm font-medium hover:bg-gray-300 dark:hover:bg-gray-600 transition-colors whitespace-nowrap"
              >
                Stop
              </button>
            ) : (
              <button
                type="submit"
                disabled={!input.trim() || !activeChatId}
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
