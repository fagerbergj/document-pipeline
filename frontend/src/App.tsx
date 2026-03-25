import { Routes, Route } from 'react-router-dom'
import NavBar from './components/NavBar'
import Dashboard from './pages/Dashboard'
import Document from './pages/Document'
import Contexts from './pages/Contexts'

export default function App() {
  return (
    <div className="min-h-screen bg-gray-50">
      <NavBar />
      <main className="max-w-6xl mx-auto px-4 py-6">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/documents/:id" element={<Document />} />
          <Route path="/contexts" element={<Contexts />} />
        </Routes>
      </main>
    </div>
  )
}
