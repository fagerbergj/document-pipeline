import { useEffect, useRef, useState } from 'react'
// useRef kept for prevFilters
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import DocKebabMenu from '../components/DocKebabMenu'
import UploadModal from '../components/UploadModal'
import RecordModal from '../components/RecordModal'
import SearchBar, { buildLuceneQuery } from '../components/SearchBar'

type SortKey = 'pipeline' | 'title_asc' | 'title_desc' | 'created_asc' | 'created_desc'

const SORT_COLS: Record<string, { asc: SortKey; desc: SortKey }> = {
  title:   { asc: 'title_asc',    desc: 'title_desc'   },
  created: { asc: 'created_asc',  desc: 'created_desc' },
}

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100]

export default function Dashboard() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const sort   = (searchParams.get('sort') ?? 'pipeline') as SortKey
  const s      = searchParams.get('s') ?? ''
  const status = searchParams.get('status') ?? ''
  const stage  = searchParams.get('stage') ?? ''
  const adv    = searchParams.get('adv') ?? ''
  const q      = adv || buildLuceneQuery(s, status, stage)
  const hasSearch = !!(s || status || stage || adv)

  const [pageSize, setPageSizeState] = useState(20)

  // Stack of page tokens for prev navigation; current token is last entry (null = first page)
  const [tokenStack, setTokenStack] = useState<(string | null)[]>([null])
  const currentToken = tokenStack[tokenStack.length - 1]

  const resetPage = () => setTokenStack([null])
  const prevFilters = useRef({ sort, q })
  useEffect(() => {
    const p = prevFilters.current
    if (p.sort !== sort || p.q !== q) {
      resetPage()
      prevFilters.current = { sort, q }
    }
  }, [sort, q])

  const { data: pipelineDetail } = useQuery({
    queryKey: ['pipeline'],
    queryFn: () => api.pipeline(),
    staleTime: Infinity,
  })
  const pipelineStages = (pipelineDetail?.stages ?? []).map(s => s.name)

  const { data: page, isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['documents', sort, q, pageSize, currentToken],
    queryFn: () => api.documents({
      sort,
      page_size: pageSize,
      page_token: currentToken ?? undefined,
      q: q || undefined,
    }),
    refetchInterval: 3_000,
  })

  const docs = page?.data ?? []
  const seriesList = [...new Set(docs.map(d => d.series).filter((s): s is string => !!s))]
  const jobIds = docs.map(d => d.current_job_id).filter(Boolean).join(',')

  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-for-page', jobIds],
    queryFn: () => api.jobs({ job_id: jobIds, page_size: pageSize }),
    enabled: !!jobIds,
    refetchInterval: (query) => {
      const active = query.state.data?.data.some(
        j => j.status === 'running' || j.status === 'pending'
      )
      return active ? 2_000 : 10_000
    },
  })

  const jobById = Object.fromEntries((jobsPage?.data ?? []).map(j => [j.id, j]))
  const hasNext = !!page?.next_page_token
  const hasPrev = tokenStack.length > 1

  function goNext() {
    if (page?.next_page_token) setTokenStack(s => [...s, page.next_page_token!])
  }
  function goPrev() {
    setTokenStack(s => s.slice(0, -1))
  }
  function setPageSize(n: number) {
    setPageSizeState(n)
    resetPage()
  }

  function setSort(col: string) {
    const next = new URLSearchParams(searchParams)
    const cols = SORT_COLS[col]
    if (!cols) { next.delete('sort'); setSearchParams(next); return }
    const newSort = sort === cols.asc ? cols.desc : cols.asc
    next.set('sort', newSort)
    setSearchParams(next)
    resetPage()
  }

  function sortIcon(col: string) {
    const cols = SORT_COLS[col]
    if (!cols) return null
    if (sort === cols.asc) return <span className="ml-1 text-blue-500">↑</span>
    if (sort === cols.desc) return <span className="ml-1 text-blue-500">↓</span>
    return <span className="ml-1 text-gray-300">↕</span>
  }

  const updatedTime = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : ''
  const [showUpload, setShowUpload] = useState(false)
  const [showRecord, setShowRecord] = useState(false)

  return (
    <div className="h-full">
      {showUpload && <UploadModal onClose={() => setShowUpload(false)} />}
      {showRecord && <RecordModal onClose={() => setShowRecord(false)} />}
      {/* Header bar */}
      <div className="px-4 py-3 sm:px-6 sm:py-4 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <h1 className="text-lg font-semibold text-gray-900 dark:text-white shrink-0">Documents</h1>
          <div className="flex items-center gap-3 flex-wrap flex-1">
            <SearchBar stages={pipelineStages} />
          </div>
          <div className="flex items-center gap-3 shrink-0">
            <button
              onClick={() => setShowRecord(true)}
              className="px-3 py-1.5 text-sm font-medium border border-red-300 text-red-600 dark:text-red-400 rounded-lg hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors"
            >
              ● Record
            </button>
            <button
              onClick={() => setShowUpload(true)}
              className="px-3 py-1.5 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 transition-colors"
            >
              Upload
            </button>
            <span className="text-xs text-gray-400 dark:text-gray-500 hidden sm:block">Updated {updatedTime}</span>
          </div>
        </div>
      </div>

      {/* Table */}
      <div className="p-6">
        {isLoading ? <LoadingSpinner /> : (
          <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 overflow-hidden">
            {!docs.length ? (
              <div className="py-16 text-center text-gray-400 dark:text-gray-500 text-sm">
                {hasSearch ? 'No documents match the current filters.' : 'No documents yet.'}
              </div>
            ) : (
              <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-gray-100 dark:border-gray-700">
                    <th className="text-left px-4 py-3">
                      <button onClick={() => setSort('title')} className="flex items-center text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide hover:text-gray-600 dark:hover:text-gray-300">
                        Title {sortIcon('title')}
                      </button>
                    </th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide hidden md:table-cell">Series</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">Stage</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide">Status</th>
                    <th className="text-left px-4 py-3 hidden sm:table-cell">
                      <button onClick={() => setSort('created')} className="flex items-center text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide hover:text-gray-600 dark:hover:text-gray-300">
                        Received {sortIcon('created')}
                      </button>
                    </th>
                    <th className="w-10" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-50 dark:divide-gray-700">
                  {docs.map(doc => {
                    const job = doc.current_job_id ? jobById[doc.current_job_id] : undefined
                    return (
                    <tr key={doc.id}
                      onClick={() => navigate(`/documents/${doc.id}`)}
                      className="hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors group cursor-pointer">
                      <td className="px-4 py-3">
                        <InlineTitle
                          docId={doc.id}
                          title={doc.title ?? null}
                          onSaved={() => qc.invalidateQueries({ queryKey: ['documents'] })}
                        />
                      </td>
                      <td className="px-4 py-3 hidden md:table-cell" onClick={e => e.stopPropagation()}>
                        <InlineSeries
                          docId={doc.id}
                          series={doc.series ?? null}
                          seriesList={seriesList}
                          onSaved={() => qc.invalidateQueries({ queryKey: ['documents'] })}
                        />
                      </td>
                      <td className="px-4 py-3">
                        <span className="text-xs font-mono text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded">{job?.stage ?? '—'}</span>
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge state={job?.status ?? ''} />
                      </td>
                      <td className="px-4 py-3 text-sm text-gray-400 dark:text-gray-500 hidden sm:table-cell">
                        {doc.created_at.slice(0, 16).replace('T', ' ')}
                      </td>
                      <td className="px-2 py-3 text-right" onClick={e => e.stopPropagation()}>
                        <DocKebabMenu
                          docId={doc.id}
                          onDelete={() => qc.invalidateQueries({ queryKey: ['documents'] })}
                          onSuccess={() => qc.invalidateQueries({ queryKey: ['documents'] })}
                          buttonClassName="w-9 h-9 sm:w-7 sm:h-7 flex items-center justify-center rounded text-gray-300 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 opacity-0 group-hover:opacity-100 transition-all text-base leading-none"
                        />
                      </td>
                    </tr>
                    )
                  })}
                </tbody>
              </table>
              </div>
            )}
            {/* Pagination footer */}
            {(hasPrev || hasNext || docs.length > 0) && (
              <div className="flex items-center justify-between px-4 py-3 border-t border-gray-100 dark:border-gray-700">
                <div className="flex items-center gap-2">
                  {!hasSearch && <>
                    <span className="text-xs text-gray-400 dark:text-gray-500">Rows per page</span>
                    <select
                      value={pageSize}
                      onChange={e => setPageSize(Number(e.target.value))}
                      className="text-xs border border-gray-200 dark:border-gray-600 rounded px-1.5 py-1 focus:outline-none focus:ring-1 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100"
                    >
                      {PAGE_SIZE_OPTIONS.map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                  </>}
                </div>
                <div className="flex items-center gap-1">
                  <button
                    onClick={goPrev}
                    disabled={!hasPrev}
                    className="px-3 py-1.5 text-sm sm:px-2.5 sm:py-1 sm:text-xs text-gray-600 dark:text-gray-300 border border-gray-200 dark:border-gray-600 rounded hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    ← Prev
                  </button>
                  <span className="px-2 text-xs text-gray-400 dark:text-gray-500">page {tokenStack.length}</span>
                  <button
                    onClick={goNext}
                    disabled={!hasNext}
                    className="px-3 py-1.5 text-sm sm:px-2.5 sm:py-1 sm:text-xs text-gray-600 dark:text-gray-300 border border-gray-200 dark:border-gray-600 rounded hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    Next →
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}


function InlineSeries({ docId, series, seriesList, onSaved }: {
  docId: string
  series: string | null
  seriesList: string[]
  onSaved: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(series ?? '')
  const [open, setOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const pickingRef = useRef(false)
  const mut = useMutation({
    mutationFn: (s: string) => api.updateDocument(docId, { series: s || null }),
    onSuccess: () => { onSaved(); setEditing(false); setOpen(false) },
  })

  useEffect(() => {
    if (editing) { inputRef.current?.select(); setOpen(true) }
  }, [editing])

  const save = (v: string) => { setOpen(false); mut.mutate(v) }
  const startEdit = (e: React.MouseEvent) => { e.stopPropagation(); setValue(series ?? ''); setEditing(true) }

  const filtered = seriesList.filter(s => s.toLowerCase().includes(value.toLowerCase()) && s !== value)

  if (editing) {
    return (
      <span className="relative" onClick={e => e.stopPropagation()}>
        <input
          ref={inputRef}
          value={value}
          onChange={e => { setValue(e.target.value); setOpen(true) }}
          onBlur={() => { if (!pickingRef.current) save(value) }}
          onKeyDown={e => {
            if (e.key === 'Enter') { e.preventDefault(); save(value) }
            if (e.key === 'Escape') { setValue(series ?? ''); setEditing(false); setOpen(false) }
          }}
          className="text-sm border-b border-blue-400 bg-transparent focus:outline-none w-full"
          disabled={mut.isPending}
          autoFocus
        />
        {open && filtered.length > 0 && (
          <ul className="absolute z-50 top-full left-0 mt-1 min-w-[160px] bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg shadow-lg py-1 text-sm">
            {filtered.map(s => (
              <li key={s}
                onMouseDown={e => { e.preventDefault(); pickingRef.current = true; setValue(s); save(s) }}
                className="px-3 py-1.5 cursor-pointer hover:bg-gray-100 dark:hover:bg-gray-700 text-gray-800 dark:text-gray-100">
                {s}
              </li>
            ))}
          </ul>
        )}
      </span>
    )
  }

  return (
    <span className="flex items-center gap-1.5 group/series">
      <span onClick={startEdit} className="text-sm text-gray-500 dark:text-gray-400 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors cursor-text">
        {series || <span className="text-gray-300 dark:text-gray-600 italic font-normal text-xs">none</span>}
      </span>
      <button onClick={startEdit}
        className="opacity-0 group-hover/series:opacity-100 text-gray-400 hover:text-gray-600 transition-opacity text-xs"
        title="Set series">
        ✎
      </button>
    </span>
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
    mutationFn: (t: string) => api.updateDocument(docId, { title: t }),
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
      <span onClick={startEdit} className="text-sm font-medium text-gray-800 dark:text-gray-100 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors cursor-text">
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
