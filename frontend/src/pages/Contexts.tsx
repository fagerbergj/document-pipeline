import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import LoadingSpinner from '../components/LoadingSpinner'
import type { ContextEntry } from '../types'

export default function Contexts() {
  const qc = useQueryClient()
  const { data: page, isLoading } = useQuery({
    queryKey: ['contexts'],
    queryFn: () => api.contexts(),
  })
  const entries = page?.data ?? []

  const [name, setName] = useState('')
  const [text, setText] = useState('')
  const [adding, setAdding] = useState(false)

  const refresh = () => qc.invalidateQueries({ queryKey: ['contexts'] })

  const createMut = useMutation({
    mutationFn: () => api.createContext(name, text),
    onSuccess: () => { refresh(); setName(''); setText(''); setAdding(false) },
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteContext(id),
    onSuccess: refresh,
  })

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
        <h1 className="text-lg font-semibold text-gray-900 dark:text-white">Context Library</h1>
        <button onClick={() => setAdding(a => !a)}
          className="px-3 py-1.5 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700">
          {adding ? 'Cancel' : '+ New'}
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-6 space-y-3">
        {/* New entry form */}
        {adding && (
          <div className="bg-white dark:bg-gray-800 rounded-xl border border-blue-200 dark:border-blue-700 ring-1 ring-blue-100 dark:ring-blue-800 p-4">
            <div className="text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wide mb-3">New context</div>
            <input value={name} onChange={e => setName(e.target.value)} placeholder="Name"
              className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 mb-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400" />
            <textarea value={text} onChange={e => setText(e.target.value)} rows={5}
              placeholder="Context text…"
              className="w-full text-sm font-mono border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 resize-y mb-3 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400" />
            <button onClick={() => createMut.mutate()} disabled={!name || !text || createMut.isPending}
              className="px-4 py-1.5 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 disabled:opacity-50">
              Save
            </button>
          </div>
        )}

        {isLoading && <LoadingSpinner />}

        {!isLoading && !entries.length && !adding && (
          <div className="py-16 text-center text-gray-400 dark:text-gray-500 text-sm">
            No saved contexts yet. Click <strong>+ New</strong> to add one.
          </div>
        )}

        {entries.map(entry => (
          <ContextEntryCard key={entry.id} entry={entry}
            onDelete={() => deleteMut.mutate(entry.id)}
            onSave={refresh} />
        ))}
      </div>
    </div>
  )
}

function ContextEntryCard({ entry, onDelete, onSave }: { entry: ContextEntry; onDelete: () => void; onSave: () => void }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(entry.name)
  const [text, setText] = useState(entry.text)

  const mut = useMutation({
    mutationFn: () => api.updateContext(entry.id, { name, text }),
    onSuccess: () => { onSave(); setEditing(false) },
  })

  return (
    <div className="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 overflow-hidden">
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-100 dark:border-gray-700">
        {editing ? (
          <input value={name} onChange={e => setName(e.target.value)}
            className="text-sm font-medium border-b border-blue-400 bg-transparent focus:outline-none flex-1 mr-3" />
        ) : (
          <span className="text-sm font-medium text-gray-800 dark:text-gray-100">{entry.name}</span>
        )}
        <div className="flex gap-3">
          <button onClick={() => { setEditing(e => !e); setName(entry.name); setText(entry.text) }}
            className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors">
            {editing ? 'Cancel' : 'Edit'}
          </button>
          <button onClick={onDelete}
            className="text-xs text-red-400 hover:text-red-600 transition-colors">
            Delete
          </button>
        </div>
      </div>

      <div className="px-4 py-3">
        {editing ? (
          <>
            <textarea value={text} onChange={e => setText(e.target.value)} rows={6}
              className="w-full text-sm font-mono border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 resize-y mb-3 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100" />
            <button onClick={() => mut.mutate()} disabled={mut.isPending}
              className="px-4 py-1.5 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 disabled:opacity-50">
              Save
            </button>
          </>
        ) : (
          <pre className="text-xs text-gray-600 dark:text-gray-300 font-mono whitespace-pre-wrap line-clamp-4">{entry.text}</pre>
        )}
      </div>
    </div>
  )
}
