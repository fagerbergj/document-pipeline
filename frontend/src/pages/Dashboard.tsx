import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge'
import LoadingSpinner from '../components/LoadingSpinner'
import type { Counts } from '../types'

const STATE_ORDER = ['pending', 'running', 'waiting', 'error', 'done']

export default function Dashboard() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()

  const stage = searchParams.get('stage') ?? ''
  const state = searchParams.get('state') ?? ''
  const sort = searchParams.get('sort') ?? 'created_desc'

  const { data: counts, dataUpdatedAt } = useQuery({
    queryKey: ['counts'],
    queryFn: api.counts,
    refetchInterval: 10_000,
  })

  const { data: stages } = useQuery({
    queryKey: ['stages'],
    queryFn: api.stages,
  })

  const { data: docs, isLoading } = useQuery({
    queryKey: ['documents', stage, state, sort],
    queryFn: () => api.documents({ stage: stage || undefined, state: state || undefined, sort }),
    refetchInterval: 10_000,
  })

  function setFilter(key: string, value: string) {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next)
  }

  const updatedTime = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : ''

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-bold text-gray-800">Dashboard</h1>
        <span className="text-xs text-gray-400">Updated {updatedTime}</span>
      </div>

      {/* Status count cards */}
      <div className="flex flex-wrap gap-3 mb-6">
        {STATE_ORDER.map(s => {
          const n = (counts as Counts | undefined)?.[s as keyof Counts] ?? 0
          if (n === 0) return null
          return (
            <button key={s}
              onClick={() => setFilter('state', state === s ? '' : s)}
              className={`bg-white rounded-lg shadow-sm px-4 py-3 text-left cursor-pointer border-2 transition-colors ${state === s ? 'border-gray-400' : 'border-transparent hover:border-gray-200'}`}>
              <span className="block text-2xl font-bold text-gray-800">{n}</span>
              <StatusBadge state={s} />
            </button>
          )
        })}
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-2 mb-4">
        <select value={stage} onChange={e => setFilter('stage', e.target.value)}
          className="text-sm border border-gray-300 rounded px-2 py-1.5 bg-white">
          <option value="">All stages</option>
          {stages?.stages.map(s => <option key={s} value={s}>{s}</option>)}
        </select>
        <select value={state} onChange={e => setFilter('state', e.target.value)}
          className="text-sm border border-gray-300 rounded px-2 py-1.5 bg-white">
          <option value="">All states</option>
          {['pending','running','waiting','error','done'].map(s => <option key={s} value={s}>{s}</option>)}
        </select>
        <select value={sort} onChange={e => setFilter('sort', e.target.value)}
          className="text-sm border border-gray-300 rounded px-2 py-1.5 bg-white">
          <option value="created_desc">Newest first</option>
          <option value="created_asc">Oldest first</option>
          <option value="title_asc">Title A-Z</option>
          <option value="title_desc">Title Z-A</option>
        </select>
        {(stage || state || sort !== 'created_desc') && (
          <button onClick={() => setSearchParams({})} className="text-sm text-gray-500 hover:text-gray-700 px-2">Clear</button>
        )}
      </div>

      {/* Document list */}
      {isLoading ? <LoadingSpinner /> : (
        <div className="bg-white rounded-lg shadow-sm overflow-hidden">
          {!docs?.length ? (
            <p className="text-center text-gray-500 py-12">No documents</p>
          ) : (
            <table className="w-full">
              <thead>
                <tr className="bg-gray-50 border-b border-gray-200">
                  <th className="text-left px-4 py-2.5 text-xs font-semibold text-gray-500 uppercase tracking-wide">Title</th>
                  <th className="text-left px-4 py-2.5 text-xs font-semibold text-gray-500 uppercase tracking-wide">Stage</th>
                  <th className="text-left px-4 py-2.5 text-xs font-semibold text-gray-500 uppercase tracking-wide">State</th>
                  <th className="text-left px-4 py-2.5 text-xs font-semibold text-gray-500 uppercase tracking-wide hidden sm:table-cell">Received</th>
                </tr>
              </thead>
              <tbody>
                {docs.map(doc => (
                  <tr key={doc.id} onClick={() => navigate(`/documents/${doc.id}`)}
                    className="border-b border-gray-100 hover:bg-gray-50 cursor-pointer transition-colors">
                    <td className="px-4 py-3 text-sm">
                      <span className="font-medium text-gray-800">{doc.title || <span className="text-gray-400 italic">untitled</span>}</span>
                      {doc.needs_context && <span className="ml-2 text-xs text-red-600 font-medium">needs context</span>}
                    </td>
                    <td className="px-4 py-3 text-sm text-gray-600 font-mono">{doc.current_stage}</td>
                    <td className="px-4 py-3"><StatusBadge state={doc.stage_state} /></td>
                    <td className="px-4 py-3 text-sm text-gray-500 hidden sm:table-cell">{doc.created_at.slice(0,16).replace('T',' ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  )
}
