import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useState, type ReactNode } from 'react'
import EditableOutput from './EditableOutput'
import DiffView from './DiffView'

// MessagePart is one ordered chunk of an assistant message — model text,
// out-of-band reasoning (collapsible), a tool call, or a pending
// human-in-the-loop confirmation card.
export type MessagePart = TextPart | ThinkingPart | ToolCallPart | ConfirmationPart

export interface TextPart {
  kind: 'text'
  text: string
}

export interface ThinkingPart {
  kind: 'thinking'
  text: string
}

export interface ToolCallPart {
  kind: 'tool_call'
  name: string
  args: Record<string, unknown>
  // result is set once the tool returns; while undefined we render a
  // "running…" indicator inside the collapsed block.
  result?: unknown
}

export interface ConfirmationPart {
  kind: 'confirmation'
  callId: string
  toolName: string
  hint: string
  // Stage payload fields surfaced by update_document. Other tools may use
  // different keys, but we currently only render the doc-edit shape.
  field: string
  stage: string
  before: string
  after: string
  status: 'pending' | 'approved' | 'rejected'
}

// AssistantParts renders a sequence of MessageParts in order — text parts
// via AssistantText (which intercepts <think> tags), tool_call parts via
// ToolBlock, confirmation parts via ConfirmationBlock. Used by the chat
// page and document live log.
//
// onDecideConfirmation is the chat page's approve/reject handler; it is
// only passed by the chat page (the doc live-log doesn't surface
// confirmations, since the worker SSE doesn't emit them).
export function AssistantParts({ parts, showCursor, onDecideConfirmation }: {
  parts: MessagePart[]
  showCursor?: boolean
  onDecideConfirmation?: (callId: string, confirmed: boolean, content?: string) => void
}) {
  const last = parts[parts.length - 1]
  // Only show the blinking cursor when streaming text. Rendering it after
  // a collapsed tool block looks orphaned.
  const cursorAfterText = showCursor && last?.kind === 'text'
  return (
    <>
      {parts.map((p, i) => {
        if (p.kind === 'text') return <AssistantText key={i} text={p.text} />
        if (p.kind === 'thinking') return <ThinkBlock key={i} text={p.text} />
        if (p.kind === 'tool_call') return <ToolBlock key={i} part={p} />
        return <ConfirmationBlock key={i} part={p} onDecide={onDecideConfirmation} />
      })}
      {cursorAfterText && (
        <span className="inline-block w-1.5 h-4 bg-gray-400 animate-pulse ml-0.5 align-middle" />
      )}
    </>
  )
}

// appendStreamingPart merges a streamed chunk into the last part if it has
// the same kind, otherwise opens a new part. Used for both regular tokens
// and out-of-band thinking — the two share the {kind, text} shape and the
// same coalescing rule.
export function appendStreamingPart(parts: MessagePart[], kind: 'text' | 'thinking', text: string): MessagePart[] {
  const next = [...parts]
  const last = next[next.length - 1]
  if (last && last.kind === kind) {
    next[next.length - 1] = { ...last, text: last.text + text }
  } else {
    next.push({ kind, text })
  }
  return next
}

export const appendTextPart = (parts: MessagePart[], text: string) => appendStreamingPart(parts, 'text', text)
export const appendThinkingPart = (parts: MessagePart[], text: string) => appendStreamingPart(parts, 'thinking', text)

// appendToolCall pushes a new tool_call part with no result yet.
export function appendToolCall(parts: MessagePart[], name: string, args: Record<string, unknown>): MessagePart[] {
  return [...parts, { kind: 'tool_call', name, args }]
}

// fillToolResult finds the most recent tool_call part with a matching name
// that doesn't yet have a result, and attaches the result to it.
export function fillToolResult(parts: MessagePart[], name: string, result: unknown): MessagePart[] {
  const next = [...parts]
  for (let i = next.length - 1; i >= 0; i--) {
    const p = next[i]
    if (p.kind === 'tool_call' && p.name === name && p.result === undefined) {
      next[i] = { ...p, result }
      return next
    }
  }
  return next
}

// appendConfirmation pushes a new pending confirmation card.
export function appendConfirmation(parts: MessagePart[], part: Omit<ConfirmationPart, 'kind' | 'status'>): MessagePart[] {
  return [...parts, { kind: 'confirmation', status: 'pending', ...part }]
}

// markConfirmation updates the status of an existing confirmation card by
// callId. No-op if no matching card is found.
export function markConfirmation(parts: MessagePart[], callId: string, status: ConfirmationPart['status']): MessagePart[] {
  return parts.map(p => (p.kind === 'confirmation' && p.callId === callId ? { ...p, status } : p))
}

// partsToText concatenates all text parts into a plain-text rendition,
// suitable for copy/download. Tool call payloads are omitted.
export function partsToText(parts: MessagePart[]): string {
  return parts.filter(p => p.kind === 'text').map(p => (p as TextPart).text).join('')
}

// AssistantText renders model-emitted text as markdown. Reasoning traces
// arrive on a separate SSE event and render via ThinkBlock.
export function AssistantText({ text }: { text: string }) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
    </div>
  )
}

// ThinkBlock renders out-of-band reasoning (the `thinking` SSE event) as a
// collapsed-by-default block. Content is plain text — reasoning traces are
// not parsed as markdown to keep rendering robust against streaming.
function ThinkBlock({ text }: { text: string }) {
  return (
    <details className="my-2 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/40 not-prose">
      <summary className="cursor-pointer select-none px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
        thinking
      </summary>
      <div className="px-3 pb-3 pt-1 text-xs text-gray-600 dark:text-gray-300 whitespace-pre-wrap font-mono">
        {text}
      </div>
    </details>
  )
}

// ToolBlock renders a tool call as a collapsed-by-default section showing
// the tool name + args; expanded view shows the result (or "running…" if
// the tool hasn't returned yet).
export function ToolBlock({ part }: { part: ToolCallPart }) {
  const argSummary = summarizeArgs(part.args)
  const isRunning = part.result === undefined
  return (
    <details className="my-2 rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 not-prose">
      <summary className="cursor-pointer select-none px-3 py-2 text-xs flex items-center gap-2">
        <code className="font-mono text-blue-600 dark:text-blue-400">{part.name}</code>
        {argSummary && <span className="text-gray-500 dark:text-gray-400 truncate">{argSummary}</span>}
        {isRunning && (
          <span className="ml-auto flex items-center gap-1 text-gray-400">
            <span className="w-1.5 h-1.5 rounded-full bg-gray-400 animate-bounce [animation-delay:-0.3s]" />
            <span className="w-1.5 h-1.5 rounded-full bg-gray-400 animate-bounce [animation-delay:-0.15s]" />
            <span className="w-1.5 h-1.5 rounded-full bg-gray-400 animate-bounce" />
          </span>
        )}
      </summary>
      <div className="px-3 pb-3 pt-1 space-y-2 text-xs">
        <Section label="args">
          <pre className="bg-gray-50 dark:bg-gray-900 rounded p-2 overflow-x-auto whitespace-pre-wrap font-mono">{prettyJSON(part.args)}</pre>
        </Section>
        {!isRunning && (
          <Section label="result">
            <pre className="bg-gray-50 dark:bg-gray-900 rounded p-2 overflow-x-auto whitespace-pre-wrap font-mono max-h-80 overflow-y-auto">{prettyJSON(part.result)}</pre>
          </Section>
        )}
      </div>
    </details>
  )
}

function Section({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-500 mb-0.5">{label}</div>
      {children}
    </div>
  )
}

// summarizeArgs returns a short one-line preview of the args for the
// collapsed summary row — currently the value of the single string arg
// most of our tools take (query or id).
function summarizeArgs(args: Record<string, unknown>): string {
  for (const key of ['query', 'id']) {
    const v = args[key]
    if (typeof v === 'string' && v) return JSON.stringify(v)
  }
  return ''
}

function prettyJSON(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

// ConfirmationBlock renders the inline approval card for a tool-call that
// requested human-in-the-loop confirmation. Shows the diff of the proposed
// change, an editable "after" textarea pre-filled with the model's proposal
// (user can tweak before approving), and Approve / Reject buttons.
//
// The "after" textarea state lives in this component because the user may
// edit it any number of times before deciding; we only push the value up
// when they click Approve.
export function ConfirmationBlock({ part, onDecide }: {
  part: ConfirmationPart
  onDecide?: (callId: string, confirmed: boolean, content?: string) => void
}) {
  const [edited, setEdited] = useState(part.after)
  const pending = part.status === 'pending'

  return (
    <div className="my-2 rounded-lg border border-amber-200 dark:border-amber-800 bg-amber-50 dark:bg-amber-950/30 not-prose">
      <div className="px-3 py-2 border-b border-amber-200 dark:border-amber-800 flex items-center gap-2">
        <span className="text-xs font-semibold text-amber-800 dark:text-amber-300 uppercase tracking-wide">
          Approval needed
        </span>
        <span className="text-xs text-amber-700 dark:text-amber-400">{part.hint}</span>
        {part.status !== 'pending' && (
          <span className={`ml-auto text-xs font-medium ${part.status === 'approved' ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>
            {part.status}
          </span>
        )}
      </div>

      <div className="p-3 space-y-3">
        <details open className="rounded border border-amber-200 dark:border-amber-800 bg-white dark:bg-gray-900">
          <summary className="cursor-pointer select-none px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-400">
            Diff
          </summary>
          <DiffView before={part.before} after={edited} />
        </details>

        <EditableOutput
          field={part.field}
          content={edited}
          editing={pending}
          onChange={setEdited}
          edited={pending && edited !== part.after}
          label={`Proposed ${part.field.replace(/_/g, ' ')}`}
          maxHeight="30vh"
        />

        {pending && (
          <div className="flex items-center gap-2">
            <button
              onClick={() => onDecide?.(part.callId, true, edited)}
              className="px-3 py-1.5 text-sm font-medium bg-green-600 text-white rounded-lg hover:bg-green-700"
            >
              Approve
            </button>
            <button
              onClick={() => onDecide?.(part.callId, false)}
              className="px-3 py-1.5 text-sm font-medium border border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-200 rounded-lg hover:bg-gray-50 dark:hover:bg-gray-700"
            >
              Reject
            </button>
            {edited !== part.after && (
              <button
                onClick={() => setEdited(part.after)}
                className="text-xs text-amber-700 dark:text-amber-400 hover:underline ml-auto"
              >
                Reset to model's proposal
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
