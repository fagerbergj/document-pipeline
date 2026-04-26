import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api'

interface Props {
  onClose: () => void
}

type Status = 'idle' | 'recording' | 'finalizing' | 'error'

// MediaRecorder emits a webm chunk every TIMESLICE_MS so the upload streams
// progressively rather than buffering the whole recording in browser memory.
const TIMESLICE_MS = 2000

export default function RecordModal({ onClose }: Props) {
  const navigate = useNavigate()
  const [status, setStatus] = useState<Status>('idle')
  const [elapsed, setElapsed] = useState(0)
  const [title, setTitle] = useState('')
  const [error, setError] = useState<string | null>(null)

  const recorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const controllerRef = useRef<ReadableStreamDefaultController<Uint8Array> | null>(null)
  const responsePromiseRef = useRef<Promise<Response> | null>(null)
  const tickRef = useRef<number | null>(null)

  useEffect(() => () => cleanup(), [])

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
      mediaStream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Microphone permission denied')
      setStatus('error')
      return
    }
    streamRef.current = mediaStream

    const recorder = new MediaRecorder(mediaStream, { mimeType: 'audio/webm;codecs=opus' })
    recorderRef.current = recorder

    const body = new ReadableStream<Uint8Array>({
      start(c) { controllerRef.current = c },
    })

    recorder.ondataavailable = async (e) => {
      if (e.data.size > 0 && controllerRef.current) {
        const buf = await e.data.arrayBuffer()
        controllerRef.current.enqueue(new Uint8Array(buf))
      }
    }
    recorder.onerror = (e) => {
      setError('Recorder error: ' + String(e))
      setStatus('error')
    }

    const filename = `recording-${new Date().toISOString().replace(/[:.]/g, '-')}.webm`
    // duplex: 'half' is required by Chrome to send a streaming request body.
    responsePromiseRef.current = fetch(
      `/api/v1/documents/stream?filename=${encodeURIComponent(filename)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'audio/webm' },
        body,
        // @ts-expect-error duplex isn't in DOM TS lib yet
        duplex: 'half',
      },
    )

    recorder.start(TIMESLICE_MS)
    setStatus('recording')
    setElapsed(0)
    tickRef.current = window.setInterval(() => setElapsed(e => e + 1), 1000)
  }

  async function stop() {
    if (tickRef.current) { clearInterval(tickRef.current); tickRef.current = null }
    setStatus('finalizing')

    const recorder = recorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      // Wait for the final dataavailable event before closing the body stream.
      await new Promise<void>((resolve) => {
        recorder.addEventListener('stop', () => resolve(), { once: true })
        recorder.stop()
      })
    }
    streamRef.current?.getTracks().forEach(t => t.stop())
    streamRef.current = null

    try { controllerRef.current?.close() } catch {}
    controllerRef.current = null

    try {
      const resp = await responsePromiseRef.current!
      if (!resp.ok) {
        const body = await resp.text().catch(() => '')
        throw new Error(body || `HTTP ${resp.status}`)
      }
      const job = await resp.json() as { document_id: string }
      if (title.trim()) {
        try { await api.updateDocument(job.document_id, { title: title.trim() }) } catch {}
      }
      onClose()
      navigate(`/documents/${job.document_id}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setStatus('error')
    }
  }

  function fmt(s: number) {
    const m = Math.floor(s / 60)
    const r = s % 60
    return `${m}:${r.toString().padStart(2, '0')}`
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onClose}>
      <div
        className="bg-white dark:bg-gray-800 rounded-2xl shadow-xl dark:shadow-black/40 w-full max-w-md mx-4 p-6"
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

        {error && (
          <div className="text-xs text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/30 border border-red-100 dark:border-red-800 rounded-lg px-3 py-2">
            {error}
          </div>
        )}

        <div className="text-xs text-gray-400 dark:text-gray-500 mt-4">
          Audio streams to the server as you record — long sessions don't fill device storage.
        </div>
      </div>
    </div>
  )
}
