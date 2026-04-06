import { useState, useEffect } from 'react'
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Layout from './components/layout/Layout'
import DashboardPage from './pages/DashboardPage'
import MembersPage from './pages/MembersPage'
import CyclesPage from './pages/CyclesPage'
import LedgerPage from './pages/LedgerPage'
import LoginPage from './pages/LoginPage'
import { AuthContext } from './context/AuthContext'
import { useAuth } from './hooks/useAuth'

const queryClient = new QueryClient()

export default function App() {
  const [user, setUser] = useState(null)
  const [loading, setLoading] = useState(true)
  
  useEffect(() => {
    // Check if user is logged in (JWT in localStorage)
    const token = localStorage.getItem('auth_token')
    if (token) {
      // TODO: Validate token with backend
      setUser({ token })
    }
    setLoading(false)
  }, [])

  if (loading) {
    return <div className="flex items-center justify-center h-screen">Loading...</div>
  }

  if (!user) {
    return <LoginPage setUser={setUser} />
  }

  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={{ user, setUser }}>
        <Router>
          <Layout>
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/members" element={<MembersPage />} />
              <Route path="/cycles" element={<CyclesPage />} />
              <Route path="/ledger" element={<LedgerPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Layout>
        </Router>
      </AuthContext.Provider>
    </QueryClientProvider>
  )
}
