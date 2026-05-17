import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  appendTextPart,
  appendToolCall,
  fillToolResult,
  partsToText,
  AssistantParts,
  AssistantText,
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

describe('AssistantText with <think>', () => {
  it('renders <think> content inside a collapsible labeled "thinking"', () => {
    render(<AssistantText text={'<think>my reasoning</think>then the answer'} />)
    expect(screen.getByText('thinking')).toBeInTheDocument()
    expect(screen.getByText('my reasoning')).toBeInTheDocument()
    expect(screen.getByText(/then the answer/)).toBeInTheDocument()
  })

  it('starts the thinking block collapsed', () => {
    const { container } = render(<AssistantText text={'<think>secret</think>visible'} />)
    const details = container.querySelector('details')
    expect(details).toBeTruthy()
    expect(details?.open).toBe(false)
  })
})
