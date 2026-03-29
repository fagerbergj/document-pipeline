import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'

const STATUSES = ['pending', 'running', 'waiting', 'error', 'done']
const STATUS_COLORS: Record<string, string> = {
  pending: 'bg-yellow-400',
  running: 'bg-blue-400',
  waiting: 'bg-cyan-400',
  error:   'bg-red-400',
  done:    'bg-green-400',
}

export default function Sidebar() {
  const { pathname } = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()

  const selectedStages = (searchParams.get('stages') ?? '').split(',').filter(Boolean)
  const selectedStatuses = (searchParams.get('statuses') ?? '').split(',').filter(Boolean)

  // Fetch pipeline stages (source of truth for stage list)
  const { data: pipelineDetail } = useQuery({
    queryKey: ['pipeline'],
    queryFn: () => api.pipeline(),
    staleTime: Infinity,
  })

  // Fetch all jobs (finite dataset) and aggregate counts client-side
  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-all'],
    queryFn: () => api.jobs({ page_size: 1000 }),
    refetchInterval: 10_000,
  })

  const jobs = jobsPage?.data ?? []
  const pipelineStages = pipelineDetail?.stages ?? []

  // Aggregate counts from jobs list
  const statusCounts: Record<string, number> = {}
  const stageCounts: Record<string, number> = {}
  for (const job of jobs) {
    statusCounts[job.status] = (statusCounts[job.status] ?? 0) + 1
    stageCounts[job.stage] = (stageCounts[job.stage] ?? 0) + 1
  }

  function toggleStage(s: string) {
    const next = new URLSearchParams(searchParams)
    const cur = selectedStages.includes(s)
      ? selectedStages.filter(x => x !== s)
      : [...selectedStages, s]
    cur.length ? next.set('stages', cur.join(',')) : next.delete('stages')
    setSearchParams(next)
  }

  function toggleStatus(s: string) {
    const next = new URLSearchParams(searchParams)
    const cur = selectedStatuses.includes(s)
      ? selectedStatuses.filter(x => x !== s)
      : [...selectedStatuses, s]
    cur.length ? next.set('statuses', cur.join(',')) : next.delete('statuses')
    setSearchParams(next)
  }

  function clearAll() {
    const next = new URLSearchParams(searchParams)
    next.delete('stages')
    next.delete('statuses')
    setSearchParams(next)
  }

  const hasFilters = selectedStages.length > 0 || selectedStatuses.length > 0

  return (
    <aside className="fixed left-0 top-0 h-full w-64 bg-gray-950 border-r border-gray-800 flex flex-col">
      {/* Brand */}
      <div className="px-5 py-4 border-b border-gray-800">
        <span className="text-sm font-semibold text-white tracking-wide">document-pipeline</span>
      </div>

      {/* Nav */}
      <nav className="px-3 py-3 border-b border-gray-800">
        <Link to="/" className={`flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors ${pathname === '/' ? 'bg-gray-800 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
          <span>Dashboard</span>
        </Link>
        <Link to="/contexts" className={`flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors ${pathname === '/contexts' ? 'bg-gray-800 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
          <span>Contexts</span>
        </Link>
        <Link to="/chat" className={`flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors ${pathname === '/chat' ? 'bg-gray-800 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
          <span>Chat</span>
        </Link>
      </nav>

      {/* Filters — only on dashboard */}
      {pathname === '/' && (
        <div className="flex-1 overflow-y-auto px-4 py-4 space-y-5">
          {hasFilters && (
            <button onClick={clearAll} className="text-xs text-gray-400 hover:text-white transition-colors">
              ✕ Clear all filters
            </button>
          )}

          {/* Status filter */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
              Status {selectedStatuses.length > 0 && <span className="ml-1 text-blue-400">({selectedStatuses.length})</span>}
            </div>
            <div className="space-y-1">
              {STATUSES.map(s => {
                const n = statusCounts[s] ?? 0
                const active = selectedStatuses.includes(s)
                return (
                  <button key={s} onClick={() => toggleStatus(s)}
                    className={`w-full flex items-center justify-between px-2 py-1.5 rounded text-sm transition-colors ${active ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${STATUS_COLORS[s]}`} />
                      <span className="capitalize">{s}</span>
                    </div>
                    {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                  </button>
                )
              })}
            </div>
          </div>

          {/* Stage filter */}
          {pipelineStages.length > 0 && (
            <div>
              <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
                Stage {selectedStages.length > 0 && <span className="ml-1 text-blue-400">({selectedStages.length})</span>}
              </div>
              <div className="space-y-1">
                {pipelineStages.map(s => {
                  const active = selectedStages.includes(s.name)
                  const n = stageCounts[s.name] ?? 0
                  return (
                    <button key={s.name} onClick={() => toggleStage(s.name)}
                      className={`w-full flex items-center justify-between px-2 py-1.5 rounded text-sm transition-colors ${active ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
                      <div className="flex items-center gap-2">
                        <span className={`w-3 h-3 rounded border flex-shrink-0 flex items-center justify-center ${active ? 'bg-blue-500 border-blue-500' : 'border-gray-600'}`}>
                          {active && <span className="text-white text-[8px] leading-none">✓</span>}
                        </span>
                        <span className="font-mono text-xs">{s.name}</span>
                      </div>
                      {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                    </button>
                  )
                })}
              </div>
            </div>
          )}
        </div>
      )}
    </aside>
  )
}
