import { useState, useRef, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'

type SortKey = 'pipeline' | 'title_asc' | 'title_desc' | 'created_asc' | 'created_desc'

const SORT_COLS: Record<string, { asc: SortKey; desc: SortKey }> = {
  title:   { asc: 'title_asc',    desc: 'title_desc'   },
  created: { asc: 'created_asc',  desc: 'created_desc' },
}

export default function Dashboard() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const stages = searchParams.get('stages') ?? ''
  const states = searchParams.get('states') ?? ''
  const sort = (searchParams.get('sort') ?? 'pipeline') as SortKey

  const { data: docs, isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['documents', stages, states, sort],
    queryFn: () => api.documents({
      stages: stages || undefined,
      states: states || undefined,
      sort,
    }),
    refetchInterval: 10_000,
  })

  function setSort(col: string) {
    const next = new URLSearchParams(searchParams)
    const cols = SORT_COLS[col]
    if (!cols) { next.delete('sort'); setSearchParams(next); return }
    const current = sort
    const newSort = current === cols.asc ? cols.desc : cols.asc
    next.set('sort', newSort)
    setSearchParams(next)
  }

  function sortIcon(col: string) {
    const cols = SORT_COLS[col]
    if (!cols) return null
    if (sort === cols.asc) return <span className="ml-1 text-blue-500">↑</span>
    if (sort === cols.desc) return <span className="ml-1 text-blue-500">↓</span>
    return <span className="ml-1 text-gray-300">↕</span>
  }

  const updatedTime = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : ''
  const activeFilters = [stages, states].filter(Boolean).length

  return (
    <div className="h-full">
      {/* Header bar */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 bg-white">
        <div className="flex items-center gap-3">
          <h1 className="text-lg font-semibold text-gray-900">Documents</h1>
          {activeFilters > 0 && (
            <span className="px-2 py-0.5 text-xs bg-blue-100 text-blue-700 rounded-full font-medium">
              {activeFilters} filter{activeFilters > 1 ? 's' : ''}
            </span>
          )}
        </div>
        <span className="text-xs text-gray-400">Updated {updatedTime}</span>
      </div>

      {/* Table */}
      <div className="p-6">
        {isLoading ? <LoadingSpinner /> : (
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            {!docs?.length ? (
              <div className="py-16 text-center text-gray-400 text-sm">
                {activeFilters > 0 ? 'No documents match the current filters.' : 'No documents yet.'}
              </div>
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="border-b border-gray-100">
                    <th className="text-left px-4 py-3">
                      <button onClick={() => setSort('title')} className="flex items-center text-xs font-semibold text-gray-400 uppercase tracking-wide hover:text-gray-600">
                        Title {sortIcon('title')}
                      </button>
                    </th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-gray-400 uppercase tracking-wide">Stage</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-gray-400 uppercase tracking-wide">Status</th>
                    <th className="text-left px-4 py-3 hidden sm:table-cell">
                      <button onClick={() => setSort('created')} className="flex items-center text-xs font-semibold text-gray-400 uppercase tracking-wide hover:text-gray-600">
                        Received {sortIcon('created')}
                      </button>
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-50">
                  {docs.map(doc => (
                    <tr key={doc.id}
                      onClick={() => navigate(`/documents/${doc.id}`)}
                      className="hover:bg-gray-50 transition-colors group cursor-pointer">
                      <td className="px-4 py-3">
                        <InlineTitle
                          docId={doc.id}
                          title={doc.title}
                          onSaved={() => qc.invalidateQueries({ queryKey: ['documents'] })}
                        />
                        {doc.needs_context && (
                          <span className="ml-2 text-xs text-red-500 font-medium">⚠ needs context</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <span className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-0.5 rounded">{doc.current_stage}</span>
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge state={doc.stage_state} />
                      </td>
                      <td className="px-4 py-3 text-sm text-gray-400 hidden sm:table-cell">
                        {doc.created_at.slice(0, 16).replace('T', ' ')}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function InlineTitle({ docId, title, onSaved }: {
  docId: string
  title: string | null
  onSaved: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(title ?? '')
  const inputRef = useRef<HTMLInputElement>(null)
  const mut = useMutation({
    mutationFn: (t: string) => api.updateTitle(docId, t),
    onSuccess: () => { onSaved(); setEditing(false) },
  })

  useEffect(() => {
    if (editing) inputRef.current?.select()
  }, [editing])

  const startEdit = (e: React.MouseEvent) => { e.stopPropagation(); setValue(title ?? ''); setEditing(true) }

  if (editing) {
    return (
      <input
        ref={inputRef}
        value={value}
        onChange={e => setValue(e.target.value)}
        onBlur={() => mut.mutate(value)}
        onKeyDown={e => {
          if (e.key === 'Enter') { e.preventDefault(); mut.mutate(value) }
          if (e.key === 'Escape') { setValue(title ?? ''); setEditing(false) }
        }}
        className="text-sm font-medium border-b border-blue-400 bg-transparent focus:outline-none w-full"
        disabled={mut.isPending}
        autoFocus
      />
    )
  }

  return (
    <span className="flex items-center gap-1.5 group/title">
      <span onClick={startEdit} className="text-sm font-medium text-gray-800 group-hover:text-blue-600 transition-colors cursor-text">
        {title || <span className="text-gray-400 italic font-normal">untitled</span>}
      </span>
      <button onClick={startEdit}
        className="opacity-0 group-hover/title:opacity-100 text-gray-400 hover:text-gray-600 transition-opacity text-xs"
        title="Rename">
        ✎
      </button>
    </span>
  )
}
