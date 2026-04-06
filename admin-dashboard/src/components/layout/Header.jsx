import { Link, useLocation } from 'react-router-dom'
import { useAuth } from '../../hooks/useAuth'
import { logout } from '../../services/auth'

export default function Header() {
  const { user, setUser } = useAuth()
  const location = useLocation()

  const handleLogout = () => {
    logout()
    setUser(null)
    window.location.href = '/login'
  }

  return (
    <header className="bg-white shadow-sm sticky top-0 z-50">
      <div className="flex items-center justify-between px-6 py-4">
        <div className="flex items-center gap-8">
          <h1 className="text-2xl font-bold text-green-600">MaSecure</h1>
          <nav className="flex gap-6">
            <Link
              to="/"
              className={`px-3 py-2 rounded-lg transition-colors ${
                location.pathname === '/'
                  ? 'bg-green-100 text-green-600 font-semibold'
                  : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              📊 Dashboard
            </Link>
            <Link
              to="/members"
              className={`px-3 py-2 rounded-lg transition-colors ${
                location.pathname === '/members'
                  ? 'bg-green-100 text-green-600 font-semibold'
                  : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              👥 Members
            </Link>
            <Link
              to="/cycles"
              className={`px-3 py-2 rounded-lg transition-colors ${
                location.pathname === '/cycles'
                  ? 'bg-green-100 text-green-600 font-semibold'
                  : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              📅 Cycles
            </Link>
            <Link
              to="/ledger"
              className={`px-3 py-2 rounded-lg transition-colors ${
                location.pathname === '/ledger'
                  ? 'bg-green-100 text-green-600 font-semibold'
                  : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              📖 Ledger
            </Link>
          </nav>
        </div>
        <div className="flex items-center gap-4">
          <span className="text-gray-600 text-sm">Coordinatrice</span>
          <button
            onClick={handleLogout}
            className="px-4 py-2 text-red-600 hover:bg-red-50 rounded-lg transition-colors"
          >
            Logout
          </button>
        </div>
      </div>
    </header>
  )
}
