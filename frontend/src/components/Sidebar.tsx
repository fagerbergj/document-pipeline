import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import type { Counts } from '../types'

const STATES = ['pending', 'running', 'waiting', 'error', 'done']
const STATE_COLORS: Record<string, string> = {
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
  const selectedStates = (searchParams.get('states') ?? '').split(',').filter(Boolean)

  const { data: counts } = useQuery<Counts>({
    queryKey: ['counts'],
    queryFn: api.counts,
    refetchInterval: 10_000,
  })

  const { data: stagesData } = useQuery({
    queryKey: ['stages'],
    queryFn: api.stages,
  })

  function toggleStage(s: string) {
    const next = new URLSearchParams(searchParams)
    const cur = selectedStages.includes(s)
      ? selectedStages.filter(x => x !== s)
      : [...selectedStages, s]
    cur.length ? next.set('stages', cur.join(',')) : next.delete('stages')
    setSearchParams(next)
  }

  function toggleState(s: string) {
    const next = new URLSearchParams(searchParams)
    const cur = selectedStates.includes(s)
      ? selectedStates.filter(x => x !== s)
      : [...selectedStates, s]
    cur.length ? next.set('states', cur.join(',')) : next.delete('states')
    setSearchParams(next)
  }

  function clearAll() {
    const next = new URLSearchParams(searchParams)
    next.delete('stages')
    next.delete('states')
    setSearchParams(next)
  }

  const hasFilters = selectedStages.length > 0 || selectedStates.length > 0

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
      </nav>

      {/* Filters — only on dashboard */}
      {pathname === '/' && (
        <div className="flex-1 overflow-y-auto px-4 py-4 space-y-5">
          {hasFilters && (
            <button onClick={clearAll} className="text-xs text-gray-400 hover:text-white transition-colors">
              ✕ Clear all filters
            </button>
          )}

          {/* State filter */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
              Status {selectedStates.length > 0 && <span className="ml-1 text-blue-400">({selectedStates.length})</span>}
            </div>
            <div className="space-y-1">
              {STATES.map(s => {
                const n = (counts as Record<string, number> | undefined)?.[s] ?? 0
                const active = selectedStates.includes(s)
                return (
                  <button key={s} onClick={() => toggleState(s)}
                    className={`w-full flex items-center justify-between px-2 py-1.5 rounded text-sm transition-colors ${active ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${STATE_COLORS[s]}`} />
                      <span className="capitalize">{s}</span>
                    </div>
                    {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                  </button>
                )
              })}
            </div>
          </div>

          {/* Stage filter */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">
              Stage {selectedStages.length > 0 && <span className="ml-1 text-blue-400">({selectedStages.length})</span>}
            </div>
            <div className="space-y-1">
              {stagesData?.stages.map(s => {
                const active = selectedStages.includes(s)
                const n = counts?.by_stage?.[s] ?? 0
                return (
                  <button key={s} onClick={() => toggleStage(s)}
                    className={`w-full flex items-center justify-between px-2 py-1.5 rounded text-sm transition-colors ${active ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800/50'}`}>
                    <div className="flex items-center gap-2">
                      <span className={`w-3 h-3 rounded border flex-shrink-0 flex items-center justify-center ${active ? 'bg-blue-500 border-blue-500' : 'border-gray-600'}`}>
                        {active && <span className="text-white text-[8px] leading-none">✓</span>}
                      </span>
                      <span className="font-mono text-xs">{s}</span>
                    </div>
                    {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                  </button>
                )
              })}
            </div>
          </div>
        </div>
      )}
    </aside>
  )
}
