import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import {
  appendTextPart,
  appendToolCall,
  appendConfirmation,
  fillToolResult,
  markConfirmation,
  partsToText,
  AssistantParts,
  type MessagePart,
  type ToolCallPart,
} from '../components/AgentParts'

// ── pure helpers ─────────────────────────────────────────────────────────────

describe('appendTextPart', () => {
  it('creates the first text part when parts is empty', () => {
    expect(appendTextPart([], 'hello')).toEqual([{ kind: 'text', text: 'hello' }])
  })

  it('appends to the last text part when it is the tail', () => {
    const start: MessagePart[] = [{ kind: 'text', text: 'hello' }]
    expect(appendTextPart(start, ' world')).toEqual([{ kind: 'text', text: 'hello world' }])
  })

  it('opens a new text part when the last part is a tool_call', () => {
    const start: MessagePart[] = [
      { kind: 'text', text: 'thinking…' },
      { kind: 'tool_call', name: 'rag_search', args: { query: 'foo' } },
    ]
    const out = appendTextPart(start, 'answer')
    expect(out).toHaveLength(3)
    expect(out[2]).toEqual({ kind: 'text', text: 'answer' })
  })

  it('does not mutate the input array', () => {
    const start: MessagePart[] = [{ kind: 'text', text: 'a' }]
    appendTextPart(start, 'b')
    expect(start).toEqual([{ kind: 'text', text: 'a' }])
  })
})

describe('appendToolCall', () => {
  it('appends a tool_call with no result', () => {
    const out = appendToolCall([], 'rag_search', { query: 'foo' })
    expect(out).toEqual([
      { kind: 'tool_call', name: 'rag_search', args: { query: 'foo' } },
    ])
  })
})

describe('fillToolResult', () => {
  it('attaches a result to the most recent unresolved tool_call with matching name', () => {
    const start: MessagePart[] = [
      { kind: 'tool_call', name: 'rag_search', args: { query: 'a' } },
    ]
    const out = fillToolResult(start, 'rag_search', { hits: 3 })
    expect(out[0]).toEqual({
      kind: 'tool_call', name: 'rag_search', args: { query: 'a' }, result: { hits: 3 },
    })
  })

  it('leaves earlier resolved calls alone when a newer one matches', () => {
    const start: MessagePart[] = [
      { kind: 'tool_call', name: 'rag_search', args: { query: 'a' }, result: { hits: 1 } },
      { kind: 'tool_call', name: 'rag_search', args: { query: 'b' } },
    ]
    const out = fillToolResult(start, 'rag_search', { hits: 5 })
    expect((out[0] as ToolCallPart).result).toEqual({ hits: 1 })
    expect((out[1] as ToolCallPart).result).toEqual({ hits: 5 })
  })

  it('is a no-op when no matching pending call exists', () => {
    const start: MessagePart[] = [
      { kind: 'text', text: 'hello' },
    ]
    const out = fillToolResult(start, 'rag_search', { hits: 1 })
    expect(out).toEqual(start)
  })

  it('does not mutate the input array', () => {
    const start: MessagePart[] = [
      { kind: 'tool_call', name: 'x', args: {} },
    ]
    fillToolResult(start, 'x', 'result')
    expect((start[0] as ToolCallPart).result).toBeUndefined()
  })
})

describe('partsToText', () => {
  it('concatenates only text parts', () => {
    const parts: MessagePart[] = [
      { kind: 'text', text: 'hi ' },
      { kind: 'tool_call', name: 'foo', args: {}, result: { x: 1 } },
      { kind: 'text', text: 'there' },
    ]
    expect(partsToText(parts)).toBe('hi there')
  })

  it('returns empty string for an empty parts array', () => {
    expect(partsToText([])).toBe('')
  })
})

// ── rendering ────────────────────────────────────────────────────────────────

describe('AssistantParts', () => {
  it('renders text parts as markdown and tool_call parts as collapsibles', () => {
    const parts: MessagePart[] = [
      { kind: 'text', text: 'plain text' },
      { kind: 'tool_call', name: 'rag_search', args: { query: 'foo' }, result: { hits: 1 } },
    ]
    render(<AssistantParts parts={parts} />)
    expect(screen.getByText('plain text')).toBeInTheDocument()
    expect(screen.getByText('rag_search')).toBeInTheDocument()
  })

  it('shows the streaming cursor when the last part is text', () => {
    const parts: MessagePart[] = [{ kind: 'text', text: 'hi' }]
    const { container } = render(<AssistantParts parts={parts} showCursor />)
    expect(container.querySelector('.animate-pulse')).toBeTruthy()
  })

  it('hides the streaming cursor when the last part is a tool_call', () => {
    const parts: MessagePart[] = [
      { kind: 'text', text: 'thinking' },
      { kind: 'tool_call', name: 'rag_search', args: { query: 'foo' } },
    ]
    const { container } = render(<AssistantParts parts={parts} showCursor />)
    expect(container.querySelector('.animate-pulse')).toBeFalsy()
  })

  it('renders the tool block as collapsed by default', () => {
    const parts: MessagePart[] = [
      { kind: 'tool_call', name: 'rag_search', args: { query: 'foo' }, result: { hits: 1 } },
    ]
    const { container } = render(<AssistantParts parts={parts} />)
    const details = container.querySelector('details')
    expect(details).toBeTruthy()
    expect(details?.open).toBe(false)
  })
})

describe('ThinkingPart rendering', () => {
  it('renders a thinking part inside a collapsible labeled "thinking"', () => {
    render(<AssistantParts parts={[
      { kind: 'thinking', text: 'my reasoning' },
      { kind: 'text', text: 'then the answer' },
    ]} />)
    expect(screen.getByText('thinking')).toBeInTheDocument()
    expect(screen.getByText('my reasoning')).toBeInTheDocument()
    expect(screen.getByText(/then the answer/)).toBeInTheDocument()
  })

  it('starts the thinking block collapsed', () => {
    const { container } = render(<AssistantParts parts={[
      { kind: 'thinking', text: 'secret' },
      { kind: 'text', text: 'visible' },
    ]} />)
    const details = container.querySelector('details')
    expect(details).toBeTruthy()
    expect(details?.open).toBe(false)
  })
})

// ── confirmation card ────────────────────────────────────────────────────────

describe('appendConfirmation / markConfirmation', () => {
  it('appends a pending confirmation part', () => {
    const out = appendConfirmation([], {
      callId: 'c1', toolName: 'update_document', hint: 'Replace clarified_text?',
      field: 'clarified_text', stage: 'clarify', before: 'a', after: 'b',
    })
    expect(out).toHaveLength(1)
    const c = out[0] as { kind: string; status: string }
    expect(c.kind).toBe('confirmation')
    expect(c.status).toBe('pending')
  })

  it('marks an existing confirmation status by callId', () => {
    const start = appendConfirmation([], {
      callId: 'c1', toolName: 't', hint: '', field: 'clarified_text', stage: 'clarify', before: '', after: '',
    })
    const approved = markConfirmation(start, 'c1', 'approved')
    expect((approved[0] as { status: string }).status).toBe('approved')
    // No-op for unknown callId
    expect(markConfirmation(start, 'c2', 'approved')).toEqual(start)
  })
})

describe('ConfirmationBlock rendering', () => {
  const part = {
    kind: 'confirmation' as const,
    callId: 'c1',
    toolName: 'update_document',
    hint: 'Replace clarified_text for doc abc?',
    field: 'clarified_text',
    stage: 'clarify',
    before: 'old text',
    after: 'new text',
    status: 'pending' as const,
  }

  it('shows hint, diff, and Approve/Reject buttons when pending', () => {
    render(<AssistantParts parts={[part]} onDecideConfirmation={() => {}} />)
    expect(screen.getByText(/Replace clarified_text/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reject/i })).toBeInTheDocument()
  })

  it('invokes onDecide with edited content on Approve', () => {
    const onDecide = vi.fn()
    render(<AssistantParts parts={[part]} onDecideConfirmation={onDecide} />)
    fireEvent.click(screen.getByRole('button', { name: /approve/i }))
    expect(onDecide).toHaveBeenCalledWith('c1', true, 'new text')
  })

  it('invokes onDecide with false on Reject', () => {
    const onDecide = vi.fn()
    render(<AssistantParts parts={[part]} onDecideConfirmation={onDecide} />)
    fireEvent.click(screen.getByRole('button', { name: /reject/i }))
    expect(onDecide).toHaveBeenCalledWith('c1', false)
  })

  it('hides buttons once status is approved', () => {
    const decided = { ...part, status: 'approved' as const }
    render(<AssistantParts parts={[decided]} onDecideConfirmation={() => {}} />)
    expect(screen.queryByRole('button', { name: /approve/i })).not.toBeInTheDocument()
    expect(screen.getByText(/approved/i)).toBeInTheDocument()
  })
})
