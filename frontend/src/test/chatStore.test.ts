import { describe, it, expect, beforeEach, vi } from 'vitest'

// Mock the API module so the store talks to fake fetch Responses we control.
// All tests construct their own SSE bodies via streamFrom().
vi.mock('../api', () => ({
  api: {
    sendMessage: vi.fn(),
    decideConfirmation: vi.fn(),
  },
}))

import { ChatStore } from '../state/chatStore'
import { api } from '../api'

const sendMessage = api.sendMessage as ReturnType<typeof vi.fn>
const decideConfirmation = api.decideConfirmation as ReturnType<typeof vi.fn>

// streamFrom builds a fake fetch Response whose body is an SSE stream
// emitting the given chunks. Each chunk is encoded as a UTF-8 Uint8Array.
function streamFrom(chunks: string[]): Response {
  const encoder = new TextEncoder()
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(encoder.encode(c))
      controller.close()
    },
  })
  return new Response(stream, { status: 200 })
}

const tokenFrame = (text: string) => `event: token\ndata: ${JSON.stringify({ text })}\n\n`
const confirmationFrame = (callId: string, payload: Record<string, unknown>) =>
  `event: confirmation_request\ndata: ${JSON.stringify({ call_id: callId, tool_name: 'update_document', hint: 'h', payload })}\n\n`

beforeEach(() => {
  sendMessage.mockReset()
  decideConfirmation.mockReset()
})

describe('ChatStore.submit', () => {
  it('appends user + assistant messages and folds tokens into the assistant', async () => {
    sendMessage.mockResolvedValueOnce(streamFrom([tokenFrame('hello '), tokenFrame('world')]))
    const store = new ChatStore()
    await store.submit('chat-a', 'hi')
    const s = store.get('chat-a')
    expect(s.messages).toHaveLength(2)
    expect(s.messages[0]).toMatchObject({ role: 'user', content: 'hi' })
    expect(s.messages[1].role).toBe('assistant')
    expect(s.messages[1].content).toBe('hello world')
    expect(s.streaming).toBe(false)
  })

  it('notifies only subscribers of the same chatId', async () => {
    sendMessage.mockResolvedValueOnce(streamFrom([tokenFrame('a')]))
    const store = new ChatStore()
    const a = vi.fn()
    const b = vi.fn()
    store.subscribe('chat-a', a)
    store.subscribe('chat-b', b)
    await store.submit('chat-a', 'x')
    expect(a).toHaveBeenCalled()
    expect(b).not.toHaveBeenCalled()
  })

  it('keeps chat A streaming when a different chat B is queried', async () => {
    // chat A's body never closes — simulate an in-flight stream.
    let release: () => void = () => {}
    const aBody = new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder()
        controller.enqueue(enc.encode(tokenFrame('partial')))
        release = () => { controller.close() }
      },
    })
    sendMessage.mockResolvedValueOnce(new Response(aBody, { status: 200 }))
    const store = new ChatStore()
    const promise = store.submit('chat-a', 'x')
    // Drain the microtask queue so the partial token lands.
    await new Promise(r => setTimeout(r, 0))
    expect(store.get('chat-a').streaming).toBe(true)
    expect(store.get('chat-a').messages[1].content).toBe('partial')
    // Chat B is independent.
    expect(store.get('chat-b').streaming).toBe(false)
    expect(store.get('chat-b').messages).toEqual([])
    release()
    await promise
    expect(store.get('chat-a').streaming).toBe(false)
  })
})

describe('ChatStore.seed', () => {
  it('loads history when the chat has no state', () => {
    const store = new ChatStore()
    store.seed('chat-a', [
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'hello' },
    ])
    expect(store.get('chat-a').messages).toHaveLength(2)
  })

  it('does NOT clobber a chat that already has messages', () => {
    const store = new ChatStore()
    store.seed('chat-a', [{ role: 'user', content: 'hi' }])
    store.seed('chat-a', [{ role: 'user', content: 'CLOBBERED' }])
    expect(store.get('chat-a').messages[0].content).toBe('hi')
  })
})

describe('ChatStore.decide', () => {
  it('marks the confirmation as approved and consumes the continuation stream', async () => {
    sendMessage.mockResolvedValueOnce(streamFrom([
      confirmationFrame('call-1', { field: 'clarified_text', stage: 'clarify', before: 'old', after: 'new' }),
    ]))
    const store = new ChatStore()
    await store.submit('chat-a', 'edit it')
    decideConfirmation.mockResolvedValueOnce(streamFrom([tokenFrame('done.')]))
    await store.decide('chat-a', 'call-1', true, 'final content')
    const s = store.get('chat-a')
    const parts = s.messages[1].parts!
    const confirm = parts.find(p => p.kind === 'confirmation')!
    expect((confirm as { status: string }).status).toBe('approved')
    // The continuation token landed after the confirmation card.
    expect(s.messages[1].content).toContain('done.')
  })

  it('marks the confirmation as rejected and skips streaming on reject', async () => {
    sendMessage.mockResolvedValueOnce(streamFrom([
      confirmationFrame('call-1', {}),
    ]))
    const store = new ChatStore()
    await store.submit('chat-a', 'edit it')
    // Reject path: server returns just a done event; we should not try to stream into the message.
    decideConfirmation.mockResolvedValueOnce(streamFrom([`event: done\ndata: {}\n\n`]))
    await store.decide('chat-a', 'call-1', false)
    const parts = store.get('chat-a').messages[1].parts!
    expect((parts.find(p => p.kind === 'confirmation') as { status: string }).status).toBe('rejected')
  })
})

describe('ChatStore generation guard', () => {
  it('does not resurrect a cleared chat when a late SSE event arrives', async () => {
    // chat A's body sends one token, then waits. We clear() between the
    // token landing and the stream ending — the partial token must not
    // re-create the cleared chat's state.
    let release: () => void = () => {}
    const aBody = new ReadableStream<Uint8Array>({
      start(controller) {
        const enc = new TextEncoder()
        controller.enqueue(enc.encode(tokenFrame('partial ')))
        release = () => {
          controller.enqueue(enc.encode(tokenFrame('rest')))
          controller.close()
        }
      },
    })
    sendMessage.mockResolvedValueOnce(new Response(aBody, { status: 200 }))
    const store = new ChatStore()
    const promise = store.submit('chat-a', 'hi')
    await new Promise(r => setTimeout(r, 0))
    expect(store.get('chat-a').messages[1].content).toBe('partial ')

    store.clear('chat-a')
    expect(store.get('chat-a')).toEqual({ messages: [], streaming: false, error: '' })

    // Push the second token AFTER clear. The state must remain empty —
    // the generation guard inside consumeStream's updateMessage should
    // drop the late fold.
    release()
    await promise
    expect(store.get('chat-a')).toEqual({ messages: [], streaming: false, error: '' })
  })

  it('seed is a no-op while a stream is in flight', async () => {
    // The "no clobber while streaming" branch — distinct from the
    // already-tested "no clobber when messages are already populated".
    let release: () => void = () => {}
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        release = () => controller.close()
      },
    })
    sendMessage.mockResolvedValueOnce(new Response(body, { status: 200 }))
    const store = new ChatStore()
    const promise = store.submit('chat-a', 'hi')
    await new Promise(r => setTimeout(r, 0))
    expect(store.get('chat-a').streaming).toBe(true)

    store.seed('chat-a', [{ role: 'user', content: 'CLOBBERED' }])
    // submit() already wrote a user + assistant placeholder — seed must
    // not clobber them while the stream is live.
    expect(store.get('chat-a').messages[0].content).toBe('hi')
    expect(store.get('chat-a').messages).toHaveLength(2)

    release()
    await promise
  })
})

describe('ChatStore.stop', () => {
  it('aborts the in-flight controller', async () => {
    let aborted = false
    sendMessage.mockImplementationOnce((_chatId: string, _content: string, signal: AbortSignal) => {
      return new Promise<Response>((_resolve, reject) => {
        signal.addEventListener('abort', () => {
          aborted = true
          reject(new DOMException('aborted', 'AbortError'))
        })
      })
    })
    const store = new ChatStore()
    const promise = store.submit('chat-a', 'x')
    await new Promise(r => setTimeout(r, 0))
    expect(store.get('chat-a').streaming).toBe(true)
    store.stop('chat-a')
    await promise
    expect(aborted).toBe(true)
    expect(store.get('chat-a').streaming).toBe(false)
    // AbortError is suppressed in error state.
    expect(store.get('chat-a').error).toBe('')
  })
})
