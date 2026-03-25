import { Link, useLocation } from 'react-router-dom'

export default function NavBar() {
  const { pathname } = useLocation()
  return (
    <nav className="bg-gray-900 text-white px-6 py-3 flex items-center gap-6">
      <span className="font-semibold text-white mr-2">document-pipeline</span>
      <Link to="/" className={`text-sm ${pathname === '/' ? 'text-white underline' : 'text-gray-300 hover:text-white'}`}>Dashboard</Link>
      <Link to="/contexts" className={`text-sm ${pathname === '/contexts' ? 'text-white underline' : 'text-gray-300 hover:text-white'}`}>Contexts</Link>
    </nav>
  )
}
