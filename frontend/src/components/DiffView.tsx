import { diffLines } from 'diff'
import { useEffect, useMemo, useState, type CSSProperties } from 'react'

interface DiffViewProps {
  before: string
  after: string
  maxHeight?: CSSProperties['maxHeight']
}

/**
 * DiffView renders a line-level diff between `before` and `after` with the
 * standard +/- prefixes and red/green background tinting. Used in the chat
 * confirmation card so the user can see exactly what the agent wants to
 * change before approving.
 *
 * The diff is computed with jsdiff's diffLines. We debounce `after` so the
 * O(n²)-ish diff doesn't re-run on every keystroke when the user is editing
 * large transcripts in the parent textarea.
 */
export default function DiffView({ before, after, maxHeight = '40vh' }: DiffViewProps) {
  const debouncedAfter = useDebounced(after, 150)
  const lines = useMemo(() => {
    const parts = diffLines(before, debouncedAfter)
    const rendered: Array<{ kind: 'add' | 'del' | 'ctx'; text: string }> = []
    for (const p of parts) {
      const lines = p.value.split('\n')
      // diffLines values typically end with \n; the split produces a trailing
      // empty string — drop it.
      if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop()
      const kind = p.added ? 'add' : p.removed ? 'del' : 'ctx'
      for (const line of lines) {
        rendered.push({ kind, text: line })
      }
    }
    return rendered
  }, [before, debouncedAfter])

  return (
    <div
      style={{ maxHeight }}
      className="font-mono text-xs bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 rounded-lg overflow-auto"
    >
      {lines.length === 0 && (
        <div className="px-3 py-2 text-gray-400 italic">No changes.</div>
      )}
      {lines.map((l, i) => (
        <div
          key={i}
          className={
            l.kind === 'add'
              ? 'bg-green-50 dark:bg-green-950/30 text-green-800 dark:text-green-300 px-3 py-0.5 whitespace-pre-wrap'
              : l.kind === 'del'
              ? 'bg-red-50 dark:bg-red-950/30 text-red-800 dark:text-red-300 px-3 py-0.5 whitespace-pre-wrap'
              : 'text-gray-600 dark:text-gray-400 px-3 py-0.5 whitespace-pre-wrap'
          }
        >
          <span className="select-none mr-2 text-gray-400 dark:text-gray-600">
            {l.kind === 'add' ? '+' : l.kind === 'del' ? '−' : ' '}
          </span>
          {l.text || ' '}
        </div>
      ))}
    </div>
  )
}

function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
