import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import type { ComponentPropsWithoutRef, ReactNode } from 'react'

// MessagePart is one ordered chunk of an assistant message — either model
// text (which may itself contain <think>…</think> blocks) or a tool call.
export type MessagePart = TextPart | ToolCallPart

export interface TextPart {
  kind: 'text'
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

// AssistantParts renders a sequence of MessageParts in order — text parts
// via AssistantText (which intercepts <think> tags), tool_call parts via
// ToolBlock. Used by the chat page and document live log.
export function AssistantParts({ parts, showCursor }: { parts: MessagePart[]; showCursor?: boolean }) {
  const last = parts[parts.length - 1]
  // Only show the blinking cursor when streaming text. Rendering it after
  // a collapsed tool block looks orphaned.
  const cursorAfterText = showCursor && last?.kind === 'text'
  return (
    <>
      {parts.map((p, i) => {
        if (p.kind === 'text') {
          return <AssistantText key={i} text={p.text} />
        }
        return <ToolBlock key={i} part={p} />
      })}
      {cursorAfterText && (
        <span className="inline-block w-1.5 h-4 bg-gray-400 animate-pulse ml-0.5 align-middle" />
      )}
    </>
  )
}

// appendTextPart appends streamed text to the last text part, creating a new
// text part if the last part is a tool_call (or the array is empty).
export function appendTextPart(parts: MessagePart[], text: string): MessagePart[] {
  const next = [...parts]
  const last = next[next.length - 1]
  if (last && last.kind === 'text') {
    next[next.length - 1] = { ...last, text: last.text + text }
  } else {
    next.push({ kind: 'text', text })
  }
  return next
}

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

// partsToText concatenates all text parts into a plain-text rendition,
// suitable for copy/download. Tool call payloads are omitted.
export function partsToText(parts: MessagePart[]): string {
  return parts.filter(p => p.kind === 'text').map(p => (p as TextPart).text).join('')
}

// AssistantText renders model-emitted text with markdown, intercepting
// <think>…</think> tags as collapsible reasoning traces.
export function AssistantText({ text }: { text: string }) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw]}
        components={{
          // 'think' is a non-standard tag; rehype-raw passes it through and
          // our custom renderer turns it into a collapsible.
          // @ts-expect-error — react-markdown's component map doesn't type
          // custom tag names but the runtime accepts them via rehype-raw.
          think: ThinkBlock,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  )
}

// ThinkBlock renders <think>…</think> content as a collapsed-by-default
// block. The model is asked (via the system prompt) to wrap reasoning
// between tool calls in these tags; the user sees the conclusion and can
// expand to read the reasoning.
function ThinkBlock({ children }: ComponentPropsWithoutRef<'div'>) {
  return (
    <details className="my-2 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/40 not-prose">
      <summary className="cursor-pointer select-none px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
        thinking
      </summary>
      <div className="px-3 pb-3 pt-1 text-xs text-gray-600 dark:text-gray-300 whitespace-pre-wrap font-mono">
        {children}
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
