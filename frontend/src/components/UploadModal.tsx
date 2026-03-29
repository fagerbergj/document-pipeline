import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { api } from '../api'

interface Props {
  onClose: () => void
}

export default function UploadModal({ onClose }: Props) {
  const navigate = useNavigate()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [title, setTitle] = useState('')
  const [error, setError] = useState<string | null>(null)

  const uploadMut = useMutation({
    mutationFn: () => api.uploadDocument(file!, {
      ...(title ? { title } : {}),
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
    if (f && !title) {
      const stem = f.name.replace(/\.[^.]+$/, '')
      if (stem) setTitle(stem)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
      <div
        className="bg-white rounded-2xl shadow-xl w-full max-w-md mx-4 p-6"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-base font-semibold text-gray-900">Upload document</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none">✕</button>
        </div>

        {/* File picker */}
        <div
          className={`border-2 border-dashed rounded-xl p-6 text-center cursor-pointer transition-colors mb-4 ${
            file ? 'border-blue-300 bg-blue-50' : 'border-gray-200 hover:border-gray-300'
          }`}
          onClick={() => fileRef.current?.click()}
        >
          <input
            ref={fileRef}
            type="file"
            accept=".txt,.md,.png,.jpg,.jpeg"
            className="hidden"
            onChange={e => handleFile(e.target.files?.[0] ?? null)}
          />
          {file ? (
            <div>
              <div className="text-sm font-medium text-gray-800">{file.name}</div>
              <div className="text-xs text-gray-400 mt-0.5">{(file.size / 1024).toFixed(1)} KB</div>
            </div>
          ) : (
            <div>
              <div className="text-sm text-gray-500">Click to choose a file</div>
              <div className="text-xs text-gray-400 mt-1">.txt · .md · .png · .jpg</div>
            </div>
          )}
        </div>

        {/* Title */}
        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 mb-1">Title (optional)</label>
          <input
            value={title}
            onChange={e => setTitle(e.target.value)}
            placeholder="Leave blank to auto-detect"
            className="w-full text-sm border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200"
          />
        </div>

        {error && (
          <div className="mb-4 px-3 py-2 bg-red-50 border border-red-200 rounded-lg text-xs text-red-700">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-gray-600 hover:text-gray-800">
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
