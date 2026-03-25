const colors: Record<string, string> = {
  pending: 'bg-yellow-100 text-yellow-800',
  running: 'bg-blue-100 text-blue-800',
  waiting: 'bg-cyan-100 text-cyan-800',
  error:   'bg-red-100 text-red-800',
  done:    'bg-green-100 text-green-800',
}

export default function StatusBadge({ state }: { state: string }) {
  return (
    <span className={`inline-block px-2 py-0.5 rounded text-xs font-semibold ${colors[state] ?? 'bg-gray-100 text-gray-700'}`}>
      {state}
    </span>
  )
}
