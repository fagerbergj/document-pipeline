import { useState, useRef, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '../api'

interface DocKebabMenuProps {
  docId: string
  /** Called after delete succeeds — caller decides navigation. */
  onDelete: () => void
  /** Called after stop/retry succeeds. */
  onSuccess: () => void
  /** Extra classes for the trigger button. */
  buttonClassName?: string
}

export default function DocKebabMenu({
  docId,
  onDelete,
  onSuccess,
  buttonClassName,
}: DocKebabMenuProps) {
  const [open, setOpen] = useState(false)
  const [menuPos, setMenuPos] = useState({ top: 0, right: 0 })
  const wrapRef = useRef<HTMLDivElement>(null)
  const btnRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  function openMenu() {
    if (btnRef.current) {
      const r = btnRef.current.getBoundingClientRect()
      setMenuPos({ top: r.bottom + 4, right: window.innerWidth - r.right })
    }
    setOpen(true)
  }

  useEffect(() => {
    function handler(e: MouseEvent) {
      if (
        wrapRef.current && !wrapRef.current.contains(e.target as Node) &&
        dropdownRef.current && !dropdownRef.current.contains(e.target as Node)
      ) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  // Fetch jobs for this document when the menu is open
  const { data: jobsPage } = useQuery({
    queryKey: ['jobs-for-doc', docId],
    queryFn: () => api.jobs({ document_id: docId, page_size: 50 }),
    enabled: open,
  })

  const jobs = jobsPage?.data ?? []
  // Current job: first non-done/non-error, else last by updated_at
  const STATUS_PRI: Record<string, number> = { running: 0, waiting: 1, pending: 2, error: 3, done: 4 }
  const currentJob = jobs.length
    ? [...jobs].sort((a, b) => (STATUS_PRI[a.status] ?? 5) - (STATUS_PRI[b.status] ?? 5))[0]
    : undefined

  const done = (cb: () => void) => { setOpen(false); cb() }
  const [mutError, setMutError] = useState<string | null>(null)
  const onErr = (e: unknown) => setMutError(e instanceof Error ? e.message : String(e))

  const stopMut   = useMutation({ mutationFn: () => api.putJobStatus(currentJob!.id, 'error'),   onSuccess: () => done(onSuccess), onError: onErr })
  const retryMut  = useMutation({ mutationFn: () => api.putJobStatus(currentJob!.id, 'pending'), onSuccess: () => done(onSuccess), onError: onErr })
  const deleteMut = useMutation({ mutationFn: () => api.deleteDocument(docId),                   onSuccess: () => done(onDelete),  onError: onErr })

  return (
    <div ref={wrapRef} className="relative">
      <button
        ref={btnRef}
        onClick={() => open ? setOpen(false) : openMenu()}
        className={buttonClassName ?? 'w-8 h-8 flex items-center justify-center rounded-lg text-gray-400 hover:text-gray-600 hover:bg-gray-100 transition-colors text-lg leading-none'}
      >
        ⋯
      </button>

      {open && createPortal(
        <div
          ref={dropdownRef}
          style={{ position: 'fixed', top: menuPos.top, right: menuPos.right, zIndex: 9999 }}
          className="w-48 bg-white border border-gray-200 rounded-xl shadow-lg overflow-hidden"
        >
          {mutError && (
            <div className="px-3 py-2 text-xs text-red-600 bg-red-50 border-b border-red-100">{mutError}</div>
          )}
          {currentJob?.status === 'running' && (
            <button onClick={() => stopMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-amber-700 hover:bg-amber-50">
              Stop
            </button>
          )}
          {(currentJob?.status === 'error' || currentJob?.status === 'waiting') && (
            <button onClick={() => retryMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-50">
              Retry
            </button>
          )}
          <button
            onClick={() => { if (confirm('Delete this document? This cannot be undone.')) deleteMut.mutate() }}
            className="w-full text-left px-4 py-2.5 text-sm text-red-600 hover:bg-red-50">
            Delete
          </button>
        </div>,
        document.body,
      )}
    </div>
  )
}
