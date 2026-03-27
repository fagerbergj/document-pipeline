import { Routes, Route, useLocation } from 'react-router-dom'
import Sidebar from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import Document from './pages/Document'
import Contexts from './pages/Contexts'
import Query from './pages/Query'

export default function App() {
  const { pathname } = useLocation()
  const fullWidth = pathname.startsWith('/documents/')

  return (
    <div className="flex min-h-screen bg-gray-950 text-gray-100">
      {!fullWidth && <Sidebar />}
      <div className={`flex-1 ${fullWidth ? '' : 'ml-64'} bg-gray-50 text-gray-900 min-h-screen`}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/documents/:id" element={<Document />} />
          <Route path="/contexts" element={<Contexts />} />
          <Route path="/query" element={<Query />} />
        </Routes>
      </div>
    </div>
  )
}
