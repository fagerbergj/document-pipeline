import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import LoadingSpinner from '../components/LoadingSpinner'
import type { ContextEntry } from '../types'

export default function Contexts() {
  const qc = useQueryClient()
  const { data: entries, isLoading } = useQuery({ queryKey: ['context-library'], queryFn: api.contextLibrary })
  const [name, setName] = useState('')
  const [text, setText] = useState('')

  const refresh = () => qc.invalidateQueries({ queryKey: ['context-library'] })

  const saveMut = useMutation({
    mutationFn: () => api.saveContextEntry(name, text),
    onSuccess: () => { refresh(); setName(''); setText('') }
  })
  const deleteMut = useMutation({ mutationFn: api.deleteContextEntry, onSuccess: refresh })

  if (isLoading) return <LoadingSpinner />

  return (
    <div className="max-w-2xl">
      <h1 className="text-xl font-bold text-gray-800 mb-6">Context Library</h1>

      {/* Existing entries */}
      <div className="space-y-3 mb-8">
        {!entries?.length && <p className="text-gray-500 text-sm">No saved contexts yet.</p>}
        {entries?.map(entry => (
          <ContextEntryCard key={entry.name} entry={entry} onDelete={() => deleteMut.mutate(entry.name)} onSave={refresh} />
        ))}
      </div>

      {/* New entry form */}
      <div className="bg-white rounded-lg shadow-sm p-4">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 mb-3">New Entry</h2>
        <input value={name} onChange={e => setName(e.target.value)} placeholder="Name"
          className="w-full text-sm border border-gray-300 rounded px-3 py-1.5 mb-2 focus:outline-none focus:ring-2 focus:ring-gray-300" />
        <textarea value={text} onChange={e => setText(e.target.value)} rows={4} placeholder="Context text…"
          className="w-full text-sm font-mono border border-gray-300 rounded px-3 py-2 resize-y mb-2 focus:outline-none focus:ring-2 focus:ring-gray-300" />
        <button onClick={() => saveMut.mutate()} disabled={!name || !text || saveMut.isPending}
          className="px-4 py-1.5 text-sm bg-gray-800 text-white rounded hover:bg-gray-700 disabled:opacity-50">Add</button>
      </div>
    </div>
  )
}

function ContextEntryCard({ entry, onDelete, onSave }: { entry: ContextEntry; onDelete: () => void; onSave: () => void }) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(entry.text)
  const mut = useMutation({ mutationFn: () => api.saveContextEntry(entry.name, text), onSuccess: () => { onSave(); setEditing(false) } })

  return (
    <div className="bg-white rounded-lg shadow-sm p-4">
      <div className="flex items-center justify-between mb-2">
        <span className="font-medium text-gray-800 text-sm">{entry.name}</span>
        <div className="flex gap-2">
          <button onClick={() => setEditing(e => !e)} className="text-xs text-gray-500 hover:text-gray-700">{editing ? 'Cancel' : 'Edit'}</button>
          <button onClick={onDelete} className="text-xs text-red-500 hover:text-red-700">Delete</button>
        </div>
      </div>
      {editing ? (
        <>
          <textarea value={text} onChange={e => setText(e.target.value)} rows={4}
            className="w-full text-sm font-mono border border-gray-300 rounded px-3 py-2 resize-y mb-2 focus:outline-none focus:ring-2 focus:ring-gray-300" />
          <button onClick={() => mut.mutate()} disabled={mut.isPending}
            className="px-3 py-1.5 text-sm bg-gray-800 text-white rounded hover:bg-gray-700 disabled:opacity-50">Save</button>
        </>
      ) : (
        <pre className="text-xs text-gray-600 font-mono whitespace-pre-wrap line-clamp-3">{entry.text}</pre>
      )}
    </div>
  )
}
