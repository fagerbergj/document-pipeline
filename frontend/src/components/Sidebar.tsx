import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
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

  const { data: pipelineDetail } = useQuery({
    queryKey: ['pipeline'],
    queryFn: () => api.pipeline(),
    staleTime: Infinity,
  })

  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-all'],
    queryFn: () => api.jobs({ page_size: 1000 }),
    refetchInterval: 10_000,
  })

  const pipelineStages = pipelineDetail?.stages ?? []

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

  const statusCounts: Record<string, number> = {}
  const stageCounts: Record<string, number> = {}
  for (const job of jobs) {
    statusCounts[job.status] = (statusCounts[job.status] ?? 0) + 1
    stageCounts[job.stage] = (stageCounts[job.stage] ?? 0) + 1
  }

  const [isDark, setIsDark] = useState(document.documentElement.classList.contains('dark'))

  function handleToggleDark() {
    const nowDark = document.documentElement.classList.toggle('dark')
    localStorage.setItem('theme', nowDark ? 'dark' : 'light')
    setIsDark(nowDark)
  }

  return (
    <>
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

      {/* Counts — only on dashboard */}
      {pathname === '/' && (
        <div className="flex-1 overflow-y-auto px-4 py-4 space-y-5">
          {/* Status counts */}
          <div>
            <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Status</div>
            <div className="space-y-1">
              {STATUSES.map(s => {
                const n = statusCounts[s] ?? 0
                return (
                  <div key={s} className="flex items-center justify-between px-2 py-1.5 text-sm text-gray-400">
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${STATUS_COLORS[s]}`} />
                      <span className="capitalize">{s}</span>
                    </div>
                    {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                  </div>
                )
              })}
            </div>
          </div>

          {/* Stage counts */}
          {pipelineStages.length > 0 && (
            <div>
              <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Stage</div>
              <div className="space-y-1">
                {pipelineStages.map(s => {
                  const n = stageCounts[s.name] ?? 0
                  return (
                    <div key={s.name} className="flex items-center justify-between px-2 py-1.5 text-sm text-gray-400">
                      <span className="font-mono text-xs">{s.name}</span>
                      {n > 0 && <span className="text-xs text-gray-500">{n}</span>}
                    </div>
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
