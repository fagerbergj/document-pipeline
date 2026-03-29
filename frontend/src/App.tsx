import { useEffect, useState } from 'react'
import { Routes, Route, useLocation, Navigate } from 'react-router-dom'
import Sidebar from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import Document from './pages/Document'
import Contexts from './pages/Contexts'
import Chat from './pages/Chat'

export default function App() {
  const { pathname } = useLocation()
  const fullWidth = pathname.startsWith('/documents/')
  const [sidebarOpen, setSidebarOpen] = useState(false)

  useEffect(() => {
    const stored = localStorage.getItem('theme')
    if (stored === 'dark' || (!stored && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
      document.documentElement.classList.add('dark')
    }
  }, [])

  // Close sidebar on route change on mobile
  useEffect(() => {
    setSidebarOpen(false)
  }, [pathname])

  return (
    <div className="flex min-h-screen bg-gray-950 text-gray-100">
      {!fullWidth && <Sidebar open={sidebarOpen} onClose={() => setSidebarOpen(false)} />}
      <div className={`flex-1 ${fullWidth ? '' : 'md:ml-64'} bg-gray-50 dark:bg-gray-900 text-gray-900 dark:text-white min-h-screen`}>
        {/* Hamburger button — mobile only, hidden on md+ */}
        {!fullWidth && (
          <button
            onClick={() => setSidebarOpen(true)}
            className="md:hidden fixed top-3 left-3 z-40 w-9 h-9 flex items-center justify-center rounded-lg bg-gray-800 text-gray-200 hover:bg-gray-700 transition-colors"
            aria-label="Open menu"
          >
            <span className="flex flex-col gap-1">
              <span className="block w-4 h-0.5 bg-current" />
              <span className="block w-4 h-0.5 bg-current" />
              <span className="block w-4 h-0.5 bg-current" />
            </span>
          </button>
        )}
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/documents/:id" element={<Document />} />
          <Route path="/contexts" element={<Contexts />} />
          <Route path="/chat/:sessionId?" element={<Chat />} />
          <Route path="/query" element={<Navigate to="/chat" replace />} />
        </Routes>
      </div>
    </div>
  )
}
