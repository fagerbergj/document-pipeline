import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

const STATUSES = ['pending', 'running', 'waiting', 'error', 'done']

interface Props {
  stages: string[]
}

// buildLuceneQuery assembles a Lucene query string from structured params.
export function buildLuceneQuery(s: string, status: string, stage: string): string {
  const parts: string[] = []
  if (s.trim()) {
    const term = s.trim().includes(' ') ? `"${s.trim().replace(/"/g, '\\"')}"` : s.trim()
    parts.push(`(title:${term} OR content:${term})`)
  }
  if (status) parts.push(`status:${status}`)
  if (stage)  parts.push(`stage:${stage}`)
  return parts.join(' AND ')
}

export default function SearchBar({ stages }: Props) {
  const [searchParams, setSearchParams] = useSearchParams()
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // URL params
  const s      = searchParams.get('s') ?? ''
  const status = searchParams.get('status') ?? ''
  const stage  = searchParams.get('stage') ?? ''
  const adv    = searchParams.get('adv') ?? ''   // raw lucene when non-empty
  const isAdv  = searchParams.has('adv')

  // Local text states for controlled inputs
  const [textInput, setTextInput] = useState(s)
  const [advInput, setAdvInput]   = useState(adv || buildLuceneQuery(s, status, stage))

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(searchParams)
    next.delete('page_token') // reset pagination on any filter change
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next)
  }

  function handleTextChange(val: string) {
    setTextInput(val)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setParam('s', val), 300)
  }

  function removeFilter(key: string) {
    const next = new URLSearchParams(searchParams)
    next.delete(key)
    next.delete('page_token')
    setSearchParams(next)
    if (key === 's') setTextInput('')
  }

  function enterAdvanced() {
    const assembled = buildLuceneQuery(s, status, stage)
    const next = new URLSearchParams(searchParams)
    next.delete('s'); next.delete('status'); next.delete('stage')
    next.delete('page_token')
    if (assembled) next.set('adv', assembled)
    else next.set('adv', '')
    setAdvInput(assembled)
    setSearchParams(next)
  }

  function exitAdvanced() {
    const next = new URLSearchParams(searchParams)
    next.delete('adv')
    next.delete('page_token')
    setSearchParams(next)
    setTextInput('')
  }

  function handleAdvChange(val: string) {
    setAdvInput(val)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setParam('adv', val), 300)
  }

  const activePills = [
    status && { key: 'status', label: `status: ${status}` },
    stage  && { key: 'stage',  label: `stage: ${stage}` },
  ].filter(Boolean) as { key: string; label: string }[]

  if (isAdv) {
    return (
      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-lg">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 text-xs font-mono select-none">⌘</span>
          <input
            type="text"
            value={advInput}
            onChange={e => handleAdvChange(e.target.value)}
            placeholder="status:pending AND tags:invoice"
            className="w-full pl-8 pr-3 py-1.5 text-sm font-mono border border-blue-400 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-300 dark:bg-gray-700 dark:text-gray-100 dark:border-blue-500"
          />
        </div>
        <button
          onClick={exitAdvanced}
          className="text-xs text-gray-400 hover:text-white border border-gray-600 rounded px-2 py-1.5 transition-colors whitespace-nowrap"
        >
          ← Simple
        </button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2 flex-wrap">
        {/* Text search */}
        <div className="relative">
          <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400 text-sm">🔍</span>
          <input
            type="text"
            value={textInput}
            onChange={e => handleTextChange(e.target.value)}
            placeholder="Search title & content…"
            className="pl-8 pr-3 py-1.5 text-sm border border-gray-200 dark:border-gray-600 rounded-lg w-52 focus:outline-none focus:ring-2 focus:ring-blue-300 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          />
        </div>

        {/* Status dropdown */}
        <select
          value={status}
          onChange={e => setParam('status', e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-2 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-300 dark:bg-gray-700 dark:text-gray-100"
        >
          <option value="">Status</option>
          {STATUSES.map(s => <option key={s} value={s}>{s}</option>)}
        </select>

        {/* Stage dropdown */}
        <select
          value={stage}
          onChange={e => setParam('stage', e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-2 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-300 dark:bg-gray-700 dark:text-gray-100"
        >
          <option value="">Stage</option>
          {stages.map(st => <option key={st} value={st}>{st}</option>)}
        </select>

        {/* Advanced toggle */}
        <button
          onClick={enterAdvanced}
          className="text-xs text-gray-400 hover:text-white border border-gray-600 rounded px-2 py-1.5 transition-colors whitespace-nowrap"
        >
          Advanced
        </button>
      </div>

      {/* Active filter pills */}
      {activePills.length > 0 && (
        <div className="flex items-center gap-1.5 flex-wrap">
          {activePills.map(p => (
            <span key={p.key}
              className="inline-flex items-center gap-1 px-2 py-0.5 text-xs bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-300 rounded-full">
              {p.label}
              <button onClick={() => removeFilter(p.key)} className="hover:text-blue-900 dark:hover:text-blue-100 leading-none">×</button>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
