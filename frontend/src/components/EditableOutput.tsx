import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import { type CSSProperties } from 'react'

// Fields rendered as markdown (prose stages) vs raw monospaced (transcripts etc).
const MARKDOWN_FIELDS = new Set(['clarified_text', 'summary', 'narrative_summary'])

interface EditableOutputProps {
  field: string
  content: string
  editing?: boolean
  onChange?: (text: string) => void
  // edited shows the "edited" badge when the displayed content differs from
  // baseline. Optional — callers that don't track an "original" can omit.
  edited?: boolean
  // Optional title override. Defaults to field with underscores replaced.
  label?: string
  // Optional maxHeight override on the rendered/preview panel. Defaults to 60vh.
  maxHeight?: CSSProperties['maxHeight']
}

/**
 * EditableOutput renders one of a pipeline run's outputs. In view mode it
 * renders markdown (for prose fields) or monospaced raw text. In edit mode it
 * shows the same text in a textarea and reports keystrokes via onChange.
 *
 * Used by both the document-page review section (artifact-backed content
 * fetched by the parent) and the chat confirmation card (in-memory content).
 * The component itself is content-agnostic — it does not fetch.
 */
export default function EditableOutput({
  field,
  content,
  editing = false,
  onChange,
  edited = false,
  label,
  maxHeight = '60vh',
}: EditableOutputProps) {
  const isMarkdown = MARKDOWN_FIELDS.has(field)
  const isTags = field === 'tags'
  const heading = label ?? field.replace(/_/g, ' ')

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">{heading}</div>
        {edited && <div className="text-xs text-amber-600 dark:text-amber-400">edited</div>}
      </div>
      {editing ? (
        <textarea
          value={content}
          onChange={e => onChange?.(e.target.value)}
          rows={Math.min(20, Math.max(6, content.split('\n').length))}
          style={{ maxHeight }}
          className="w-full text-sm font-mono bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg p-3 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:text-gray-100"
        />
      ) : isTags ? (
        <div className="flex flex-wrap gap-1">
          {parseTags(content).map((t, i) => (
            <span key={i} className="px-2 py-0.5 text-xs bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 border border-blue-200 dark:border-blue-800 rounded-full">{t}</span>
          ))}
        </div>
      ) : isMarkdown ? (
        <div
          style={{ maxHeight }}
          className="prose prose-sm dark:prose-invert max-w-none text-sm text-gray-700 dark:text-gray-200 overflow-y-auto bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 rounded-lg p-3"
        >
          <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw]}>{content}</ReactMarkdown>
        </div>
      ) : (
        <pre
          style={{ maxHeight }}
          className="bg-gray-50 dark:bg-gray-900 border border-gray-100 dark:border-gray-700 text-gray-800 dark:text-gray-200 rounded-lg p-3 text-xs font-mono whitespace-pre-wrap overflow-y-auto"
        >{content}</pre>
      )}
    </div>
  )
}

function parseTags(content: string): string[] {
  try { return JSON.parse(content) as string[] } catch { return [] }
}
