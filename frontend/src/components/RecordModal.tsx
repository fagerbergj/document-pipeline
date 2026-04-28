import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Link } from 'react-router-dom'
import { api } from '../api'

interface Props {
  onClose: () => void
}

type Status = 'idle' | 'recording' | 'finalizing' | 'error'

export default function RecordModal({ onClose }: Props) {
  const navigate = useNavigate()
  const [status, setStatus] = useState<Status>('idle')
  const [elapsed, setElapsed] = useState(0)
  const [title, setTitle] = useState('')
  const [series, setSeries] = useState('')
  const [additionalContext, setAdditionalContext] = useState('')
  const [linkedIds, setLinkedIds] = useState<string[]>([])
  const [contexts, setContexts] = useState<{ id: string; name: string }[]>([])
  const [error, setError] = useState<string | null>(null)

  const recorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const tickRef = useRef<number | null>(null)

  useEffect(() => {
    api.contexts().then(p => setContexts((p.data ?? []).map(e => ({ id: e.id, name: e.name })))).catch(() => {})
    return () => cleanup()
  }, [])

  function cleanup() {
    if (tickRef.current) { clearInterval(tickRef.current); tickRef.current = null }
    if (recorderRef.current && recorderRef.current.state !== 'inactive') {
      try { recorderRef.current.stop() } catch {}
    }
    streamRef.current?.getTracks().forEach(t => t.stop())
    streamRef.current = null
    recorderRef.current = null
  }

  async function start() {
    setError(null)
    let mediaStream: MediaStream
    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
        },
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Microphone permission denied')
      setStatus('error')
      return
    }
    streamRef.current = mediaStream

    const recorder = new MediaRecorder(mediaStream, { mimeType: 'audio/webm;codecs=opus' })
    recorderRef.current = recorder
    chunksRef.current = []

    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunksRef.current.push(e.data)
    }
    recorder.onerror = (e) => {
      setError('Recorder error: ' + String(e))
      setStatus('error')
    }

    recorder.start()
    setStatus('recording')
    setElapsed(0)
    tickRef.current = window.setInterval(() => setElapsed(e => e + 1), 1000)
  }

  async function stop() {
    if (tickRef.current) { clearInterval(tickRef.current); tickRef.current = null }
    setStatus('finalizing')

    const recorder = recorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      // Wait for the final dataavailable + stop sequence so chunksRef is complete.
      await new Promise<void>((resolve) => {
        recorder.addEventListener('stop', () => resolve(), { once: true })
        recorder.stop()
      })
    }
    streamRef.current?.getTracks().forEach(t => t.stop())
    streamRef.current = null

    try {
      const blob = new Blob(chunksRef.current, { type: 'audio/webm' })
      if (blob.size === 0) throw new Error('No audio captured')

      const filename = `recording-${new Date().toISOString().replace(/[:.]/g, '-')}.webm`
      const file = new File([blob], filename, { type: 'audio/webm' })
      const opts: { title?: string; series?: string; additional_context?: string; linked_contexts?: string[] } = {}
      if (title.trim()) opts.title = title.trim()
      if (series.trim()) opts.series = series.trim()
      if (additionalContext.trim()) opts.additional_context = additionalContext.trim()
      if (linkedIds.length > 0) opts.linked_contexts = linkedIds
      const job = await api.uploadDocument(file, opts)
      onClose()
      navigate(`/documents/${job.document_id}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setStatus('error')
    }
  }

  function toggleLinked(id: string) {
    setLinkedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  function fmt(s: number) {
    const m = Math.floor(s / 60)
    const r = s % 60
    return `${m}:${r.toString().padStart(2, '0')}`
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
      <div
        className="bg-white dark:bg-gray-800 rounded-2xl shadow-xl dark:shadow-black/40 w-full max-w-md mx-4 p-6 max-h-[90vh] overflow-y-auto"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-base font-semibold text-gray-900 dark:text-white">Record audio</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-xl leading-none">✕</button>
        </div>

        <div className="flex flex-col items-center mb-4">
          <div className={`text-3xl font-mono mb-3 ${status === 'recording' ? 'text-red-600' : 'text-gray-700 dark:text-gray-200'}`}>
            {fmt(elapsed)}
          </div>
          {status === 'idle' && (
            <button onClick={start}
              className="px-6 py-3 bg-red-600 text-white rounded-full text-sm font-medium hover:bg-red-700">
              ● Start recording
            </button>
          )}
          {status === 'recording' && (
            <button onClick={stop}
              className="px-6 py-3 bg-gray-700 text-white rounded-full text-sm font-medium hover:bg-gray-800">
              ■ Stop
            </button>
          )}
          {status === 'finalizing' && (
            <div className="text-sm text-gray-500">Finalizing upload…</div>
          )}
          {status === 'error' && (
            <button onClick={() => { setStatus('idle'); setError(null) }}
              className="px-4 py-2 text-xs border border-gray-300 dark:border-gray-600 rounded-lg">
              Retry
            </button>
          )}
        </div>

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
            onChange={e => setSeries(e.target.value)}
            placeholder="e.g. Colliding Worlds"
            className="w-full text-sm border border-gray-200 dark:border-gray-600 rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-200 dark:focus:ring-blue-800 dark:bg-gray-700 dark:text-gray-100 dark:placeholder-gray-400"
          />
        </div>

        <div className="mb-4">
          <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Additional context (optional)</label>
          <textarea
            value={additionalContext}
            onChange={e => setAdditionalContext(e.target.value)}
            rows={3}
            placeholder="Notes about the recording the pipeline should consider"
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
          <div className="text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/30 border border-red-100 dark:border-red-800 rounded-lg px-3 py-2">
            {error}
          </div>
        )}
      </div>
    </div>
  )
}
