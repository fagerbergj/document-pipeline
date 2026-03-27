import { useState, useRef, useEffect } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api } from '../api'

interface DocKebabMenuProps {
  docId: string
  state: string
  /** If provided, used directly. If omitted, fetched lazily on first open. */
  replayStages?: { name: string }[]
  /** Called after delete succeeds — caller decides navigation. */
  onDelete: () => void
  /** Called after stop/retry/replay succeeds. */
  onSuccess: () => void
  /** Extra classes for the trigger button (e.g. opacity/sizing). */
  buttonClassName?: string
}

export default function DocKebabMenu({
  docId,
  state,
  replayStages: providedStages,
  onDelete,
  onSuccess,
  buttonClassName,
}: DocKebabMenuProps) {
  const [open, setOpen] = useState(false)
  const [fetchedStages, setFetchedStages] = useState<{ name: string }[] | null>(null)
  const ref = useRef<HTMLDivElement>(null)

  const replayStages = providedStages ?? fetchedStages ?? []

  useEffect(() => {
    if (!open || providedStages !== undefined || fetchedStages !== null) return
    api.job(docId).then(j => setFetchedStages(j.replay_stages)).catch(() => setFetchedStages([]))
  }, [open, docId, providedStages, fetchedStages])

  useEffect(() => {
    function handler(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const done = (cb: () => void) => { setOpen(false); cb() }

  const stopMut   = useMutation({ mutationFn: () => api.postJobEvent(docId, { type: 'stop' }),              onSuccess: () => done(onSuccess) })
  const retryMut  = useMutation({ mutationFn: () => api.postJobEvent(docId, { type: 'retry' }),             onSuccess: () => done(onSuccess) })
  const replayMut = useMutation({ mutationFn: (s: string) => api.postJobEvent(docId, { type: 'replay', stage: s }), onSuccess: () => done(onSuccess) })
  const deleteMut = useMutation({ mutationFn: () => api.deleteDocument(docId),                              onSuccess: () => done(onDelete) })

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(o => !o)}
        className={buttonClassName ?? 'w-8 h-8 flex items-center justify-center rounded-lg text-gray-400 hover:text-gray-600 hover:bg-gray-100 transition-colors text-lg leading-none'}>
        ⋯
      </button>
      {open && (
        <div className="absolute right-0 top-9 w-48 bg-white border border-gray-200 rounded-xl shadow-lg z-20 overflow-hidden">
          {state === 'running' && (
            <button onClick={() => stopMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-amber-700 hover:bg-amber-50">
              Stop
            </button>
          )}
          {state === 'error' && (
            <button onClick={() => retryMut.mutate()}
              className="w-full text-left px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-50">
              Retry
            </button>
          )}
          {replayStages.length > 0 && (
            <>
              <div className="px-3 py-1.5 text-xs font-semibold text-gray-400 uppercase tracking-wide border-b border-gray-100">
                Replay from
              </div>
              {replayStages.map(s => (
                <button key={s.name}
                  onClick={() => {
                    if (confirm(`Replay from ${s.name}? This will clear downstream stage data.`))
                      replayMut.mutate(s.name)
                  }}
                  className="w-full text-left px-4 py-2 text-sm font-mono text-gray-700 hover:bg-gray-50">
                  {s.name}
                </button>
              ))}
              <div className="border-t border-gray-100" />
            </>
          )}
          <button
            onClick={() => { if (confirm('Delete this document? This cannot be undone.')) deleteMut.mutate() }}
            className="w-full text-left px-4 py-2.5 text-sm text-red-600 hover:bg-red-50">
            Delete
          </button>
        </div>
      )}
    </div>
  )
}
