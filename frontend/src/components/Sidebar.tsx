import { useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import type { JobSummary } from '../generated/types.gen'

const STATUSES = ['pending', 'running', 'waiting', 'error', 'done']
const STATUS_COLORS: Record<string, string> = {
  pending: 'bg-yellow-400',
  running: 'bg-blue-400',
  waiting: 'bg-cyan-400',
  error:   'bg-red-400',
  done:    'bg-green-400',
}

interface SidebarProps {
  open: boolean
  onClose: () => void
}

export default function Sidebar({ open, onClose }: SidebarProps) {
  const { pathname } = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()

  const activeQ = searchParams.get('q') ?? ''

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

  const pipelineStages = pipelineDetail?.stages ?? []

  // Keep only the current job per document (same priority as server pickCurrentJob).
  const statusPriority: Record<string, number> = { running: 0, waiting: 1, pending: 2, error: 3, done: 4 }
  const currentJobByDoc = new Map<string, JobSummary>()
  for (const job of jobsPage?.data ?? []) {
    const existing = currentJobByDoc.get(job.document_id)
    if (!existing) { currentJobByDoc.set(job.document_id, job); continue }
    const cur = statusPriority[existing.status] ?? 99
    const next = statusPriority[job.status] ?? 99
    if (next < cur || (next === cur && job.updated_at > existing.updated_at)) {
      currentJobByDoc.set(job.document_id, job)
    }
  }
  const jobs = Array.from(currentJobByDoc.values())

  // Aggregate counts from current jobs only
  const statusCounts: Record<string, number> = {}
  const stageCounts: Record<string, number> = {}
  for (const job of jobs) {
    statusCounts[job.status] = (statusCounts[job.status] ?? 0) + 1
    stageCounts[job.stage] = (stageCounts[job.stage] ?? 0) + 1
  }

  function setLuceneFilter(value: string) {
    const next = new URLSearchParams(searchParams)
    if (value) next.set('q', value)
    else next.delete('q')
    setSearchParams(next)
  }

  function clearAll() {
    const next = new URLSearchParams(searchParams)
    next.delete('q')
    setSearchParams(next)
  }

  const hasFilters = !!activeQ

  const [isDark, setIsDark] = useState(document.documentElement.classList.contains('dark'))

  function handleToggleDark() {
    const nowDark = document.documentElement.classList.toggle('dark')
    localStorage.setItem('theme', nowDark ? 'dark' : 'light')
    setIsDark(nowDark)
  }

  return (
    <>
      {/* Mobile backdrop */}
      {open && (
        <div
          className="md:hidden fixed inset-0 z-40 bg-black/50"
          onClick={onClose}
          aria-hidden="true"
        />
      )}

    <aside className={`fixed left-0 top-0 h-full w-64 bg-gray-950 border-r border-gray-800 flex flex-col z-50 transition-transform duration-200
      md:translate-x-0
      ${open ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}`}>
      {/* Brand */}
      <div className="px-5 py-4 border-b border-gray-800 flex items-center justify-between">
        <span className="text-sm font-semibold text-white tracking-wide">document-pipeline</span>
        <div className="flex items-center gap-2">
          <button
            onClick={handleToggleDark}
            title={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
            className="text-gray-400 hover:text-white transition-colors text-base leading-none"
          >
            {isDark ? '☀' : '☽'}
          </button>
          {/* Close button — mobile only */}
          <button
            onClick={onClose}
            className="md:hidden text-gray-400 hover:text-white transition-colors text-lg leading-none"
            aria-label="Close menu"
          >
            ✕
          </button>
        </div>
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
              ✕ Clear filter
            </button>
          )}

          {/* Status quick-filters */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Status</div>
            <div className="space-y-1">
              {STATUSES.map(s => {
                const n = statusCounts[s] ?? 0
                const luceneVal = `status:${s}`
                const active = activeQ === luceneVal
                return (
                  <button key={s} onClick={() => setLuceneFilter(active ? '' : luceneVal)}
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

          {/* Stage quick-filters */}
          {pipelineStages.length > 0 && (
            <div>
              <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Stage</div>
              <div className="space-y-1">
                {pipelineStages.map(s => {
                  const luceneVal = `stage:${s.name}`
                  const active = activeQ === luceneVal
                  const n = stageCounts[s.name] ?? 0
                  return (
                    <button key={s.name} onClick={() => setLuceneFilter(active ? '' : luceneVal)}
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
    </>
  )
}
