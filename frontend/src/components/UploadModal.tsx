import { useEffect, useRef, useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { api } from '../api'

interface Props {
  onClose: () => void
}

const AUDIO_EXTS = ['mp4', 'mp3', 'm4a', 'wav', 'webm', 'ogg', 'flac']

function isTranscribable(f: File | null): boolean {
  if (!f) return false
  const m = f.name.match(/\.([^.]+)$/)
  return !!m && AUDIO_EXTS.includes(m[1].toLowerCase())
}

export default function UploadModal({ onClose }: Props) {
  const navigate = useNavigate()
  const fileRef = useRef<HTMLInputElement>(null)
  const transcriptRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [transcript, setTranscript] = useState<File | null>(null)
  const [title, setTitle] = useState('')
  const [series, setSeries] = useState('')
  const [additionalContext, setAdditionalContext] = useState('')
  const [linkedIds, setLinkedIds] = useState<string[]>([])
  const [contexts, setContexts] = useState<{ id: string; name: string }[]>([])
  const [seriesOptions, setSeriesOptions] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.contexts().then(p => setContexts((p.data ?? []).map(e => ({ id: e.id, name: e.name })))).catch(() => {})
    api.documents({ page_size: 100 }).then(p => {
      const names = new Set<string>()
      for (const d of p.data ?? []) {
        if (d.series) names.add(d.series)
      }
      setSeriesOptions([...names].sort())
    }).catch(() => {})
  }, [])

  const uploadMut = useMutation({
    mutationFn: () => api.uploadDocument(file!, {
      ...(title.trim() ? { title: title.trim() } : {}),
      ...(series.trim() ? { series: series.trim() } : {}),
      ...(additionalContext.trim() ? { additional_context: additionalContext.trim() } : {}),
      ...(linkedIds.length > 0 ? { linked_contexts: linkedIds } : {}),
      ...(transcript ? { artifacts: [{ stage: 'transcribe', field: 'raw_text', file: transcript }] } : {}),
    }),
    onSuccess: (job) => {
      onClose()
      navigate(`/documents/${job.document_id}`)
    },
    onError: (err: Error & { status?: number; body?: { error?: string } }) => {
      setError(err.body?.error ?? err.message ?? 'Upload failed')
    },
  })

  function handleFile(f: File | null) {
    setFile(f)
    setError(null)
    // Reset the transcript when the media changes so a stale pick doesn't ride
    // along with a different file (or with a non-transcribable file).
    setTranscript(null)
    if (f && !title) {
      const stem = f.name.replace(/\.[^.]+$/, '')
      if (stem) setTitle(stem)
    }
  }

  function toggleLinked(id: string) {
    setLinkedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
      <div
        className="bg-white dark:bg-gray-800 rounded-2xl shadow-xl dark:shadow-black/40 w-full max-w-md mx-4 p-6 max-h-[90vh] overflow-y-auto"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-base font-semibold text-gray-900 dark:text-white">Upload document</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-xl leading-none">✕</button>
        </div>

        {/* File picker */}
        <div
          className={`border-2 border-dashed rounded-xl p-6 text-center cursor-pointer transition-colors mb-4 ${
            file ? 'border-blue-300 bg-blue-50 dark:bg-blue-900/20' : 'border-gray-200 dark:border-gray-600 hover:border-gray-300 dark:hover:border-gray-500'
          }`}
          onClick={() => fileRef.current?.click()}
        >
          <input
            ref={fileRef}
            type="file"
            accept=".txt,.md,.png,.jpg,.jpeg,.webm,.wav,.mp3,.m4a,.ogg,.flac,.mp4"
            className="hidden"
            onChange={e => handleFile(e.target.files?.[0] ?? null)}
          />
          {file ? (
            <div>
              <div className="text-sm font-medium text-gray-800 dark:text-gray-100">{file.name}</div>
              <div className="text-xs text-gray-400 dark:text-gray-500 mt-0.5">{(file.size / 1024).toFixed(1)} KB</div>
            </div>
          ) : (
            <div>
              <div className="text-sm text-gray-500 dark:text-gray-400">Click to choose a file</div>
              <div className="text-xs text-gray-400 dark:text-gray-500 mt-1">.txt · .md · .png · .jpg · audio (.webm/.wav/.mp3/.m4a/.ogg/.flac/.mp4)</div>
            </div>
          )}
        </div>

        {isTranscribable(file) && (
          <div className="mb-4">
            <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Transcript file (optional)</label>
            <input
              ref={transcriptRef}
              type="file"
              accept=".txt,.md,.vtt,.srt,text/*"
              className="hidden"
              onChange={e => setTranscript(e.target.files?.[0] ?? null)}
            />
            {transcript ? (
              <div className="flex items-center justify-between text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 dark:bg-gray-700 dark:text-gray-100">
                <span className="truncate">{transcript.name} <span className="text-xs text-gray-400 dark:text-gray-500">({(transcript.size / 1024).toFixed(1)} KB)</span></span>
                <button
                  onClick={() => { setTranscript(null); if (transcriptRef.current) transcriptRef.current.value = '' }}
                  className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-sm ml-2"
                >Remove</button>
              </div>
            ) : (
              <button
                onClick={() => transcriptRef.current?.click()}
                className="w-full text-sm text-gray-500 dark:text-gray-400 border border-dashed border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 hover:border-gray-300 dark:hover:border-gray-500"
              >Choose transcript file — skips whisper</button>
            )}
          </div>
        )}

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Title (optional)</label>
          <input
            value={title}
            onChange={e => setTitle(e.target.value)}
            placeholder="Leave blank to auto-detect"
            className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          />
        </div>

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Series (optional)</label>
          <input
            value={series}
            list="series-suggestions"
            onChange={e => setSeries(e.target.value)}
            placeholder="e.g. Colliding Worlds"
            className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          />
          <datalist id="series-suggestions">
            {seriesOptions.map(s => <option key={s} value={s} />)}
          </datalist>
        </div>

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Additional context (optional)</label>
          <textarea
            value={additionalContext}
            onChange={e => setAdditionalContext(e.target.value)}
            rows={3}
            placeholder="Notes about the document the pipeline should consider"
            className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          />
        </div>

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Linked contexts (optional)</label>
          {contexts.length === 0 ? (
            <div className="text-xs text-gray-400 dark:text-gray-500">
              No saved contexts —{' '}
              <Link to="/contexts" className="text-blue-500 hover:underline">create one in Context Library</Link>
            </div>
          ) : (
            <div className="space-y-1">
              {contexts.map(c => (
                <label key={c.id} className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={linkedIds.includes(c.id)}
                    onChange={() => toggleLinked(c.id)}
                    className="rounded border-gray-300 dark:border-gray-600"
                  />
                  <span className="text-sm text-gray-700 dark:text-gray-200">{c.name}</span>
                </label>
              ))}
            </div>
          )}
        </div>

        {error && (
          <div className="mb-4 px-3 py-2 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-800 rounded-lg text-xs text-red-700 dark:text-red-400">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 dark:text-gray-300 hover:text-gray-800 dark:hover:text-gray-100">
            Cancel
          </button>
          <button
            onClick={() => uploadMut.mutate()}
            disabled={!file || uploadMut.isPending}
            className="px-4 py-2 text-sm font-medium bg-gray-900 text-white rounded-lg hover:bg-gray-700 disabled:opacity-50"
          >
            {uploadMut.isPending ? 'Uploading…' : 'Upload'}
          </button>
        </div>
      </div>
    </div>
  )
}
