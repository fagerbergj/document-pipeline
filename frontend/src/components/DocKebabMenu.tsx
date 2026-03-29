import { useState, useRef, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '../api'

interface DocKebabMenuProps {
  docId: string
  onDelete: () => void
  onSuccess: () => void
  buttonClassName?: string
}

export default function DocKebabMenu({ docId, onDelete, onSuccess, buttonClassName }: DocKebabMenuProps) {
  const [open, setOpen] = useState(false)
  const [menuPos, setMenuPos] = useState({ top: 0, right: 0 })
  const [replayOpen, setReplayOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  function openMenu() {
    if (btnRef.current) {
      const r = btnRef.current.getBoundingClientRect()
      setMenuPos({ top: r.bottom + 4, right: window.innerWidth - r.right })
    }
    setReplayOpen(false)
    setOpen(true)
  }

  useEffect(() => {
    function handler(e: MouseEvent) {
      if (
        wrapRef.current && !wrapRef.current.contains(e.target as Node) &&
        dropdownRef.current && !dropdownRef.current.contains(e.target as Node)
      ) { setOpen(false); setReplayOpen(false) }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-for-doc', docId],
    queryFn: () => api.jobs({ document_id: docId, page_size: 50 }),
    enabled: open,
  })

  const jobs = jobsPage?.data ?? []
  const STATUS_PRI: Record<string, number> = { running: 0, waiting: 1, pending: 2, error: 3, done: 4 }
  const currentJob = jobs.length
    ? [...jobs].sort((a, b) => (STATUS_PRI[a.status] ?? 5) - (STATUS_PRI[b.status] ?? 5))[0]
    : undefined
  const replayableJobs = jobs.filter(j => j.status === 'done')

  const closeAll = () => { setOpen(false); setReplayOpen(false) }
  const [mutError, setMutError] = useState<string | null>(null)
  const onErr = (e: unknown) => setMutError(e instanceof Error ? e.message : String(e))

  const stopMut   = useMutation({ mutationFn: () => api.putJobStatus(currentJob!.id, 'error'),   onSuccess: () => { closeAll(); onSuccess() }, onError: onErr })
  const retryMut  = useMutation({ mutationFn: () => api.putJobStatus(currentJob!.id, 'pending'), onSuccess: () => { closeAll(); onSuccess() }, onError: onErr })
  const replayMut = useMutation({ mutationFn: (jobId: string) => api.putJobStatus(jobId, 'pending'), onSuccess: () => { closeAll(); onSuccess() }, onError: onErr })
  const deleteMut = useMutation({ mutationFn: () => api.deleteDocument(docId), onSuccess: () => { closeAll(); onDelete() }, onError: onErr })

  return (
    <div ref={wrapRef} className="relative">
      <button
        ref={btnRef}
        onClick={() => open ? closeAll() : openMenu()}
        className={buttonClassName ?? 'w-8 h-8 flex items-center justify-center rounded-lg text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors text-lg leading-none'}
      >
        ⋯
      </button>

      {open && createPortal(
        <div
          ref={dropdownRef}
          style={{ position: 'fixed', top: menuPos.top, right: menuPos.right, zIndex: 9999 }}
          className="w-48 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-lg dark:shadow-black/40 overflow-hidden"
        >
          {mutError && (
            <div className="px-3 py-2 text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/30 border-b border-red-100 dark:border-red-800">{mutError}</div>
          )}

          {currentJob?.status === 'running' && (
            <button onClick={() => stopMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-amber-700 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950/30">
              Stop
            </button>
          )}
          {(currentJob?.status === 'error' || currentJob?.status === 'waiting') && (
            <button onClick={() => retryMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-700">
              Retry
            </button>
          )}

          {/* Replay submenu */}
          {replayableJobs.length > 0 && (
            <div className="relative">
              <button
                onMouseEnter={() => setReplayOpen(true)}
                onClick={() => setReplayOpen(r => !r)}
                className="w-full text-left px-4 py-2.5 text-sm text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-700 flex items-center justify-between"
              >
                Replay
                <span className="text-gray-400 text-xs">▶</span>
              </button>
              {replayOpen && (
                <div
                  onMouseLeave={() => setReplayOpen(false)}
                  style={{ position: 'fixed', top: menuPos.top, right: menuPos.right + 192, zIndex: 10000 }}
                  className="w-36 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-lg dark:shadow-black/40 overflow-hidden"
                >
                  {replayableJobs.map(j => (
                    <button
                      key={j.id}
                      onClick={() => replayMut.mutate(j.id)}
                      disabled={replayMut.isPending}
                      className="w-full text-left px-4 py-2.5 text-sm font-mono text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-700 disabled:opacity-50"
                    >
                      {j.stage}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}

          <button
            onClick={() => { if (confirm('Delete this document? This cannot be undone.')) deleteMut.mutate() }}
            className="w-full text-left px-4 py-2.5 text-sm text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30">
            Delete
          </button>
        </div>,
        document.body,
      )}
    </div>
  )
}
