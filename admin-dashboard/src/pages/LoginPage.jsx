import { useState } from 'react'
import { Card, Input, Button, Toast } from '../components/common/Common'

export default function LoginPage({ setUser }) {
  const [formData, setFormData] = useState({ email: '', password: '' })
  const [loading, setLoading] = useState(false)
  const [toast, setToast] = useState(null)

  const handleChange = (e) => {
    setFormData({ ...formData, [e.target.name]: e.target.value })
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setLoading(true)

    try {
      // Mock login - replace with real backend call
      if (formData.email && formData.password) {
        const token = 'mock-jwt-token-' + Date.now()
        localStorage.setItem('auth_token', token)
        setUser({ token, email: formData.email })
        // App will re-render with the new user and show the dashboard
      } else {
        setToast({ type: 'error', message: 'Email and password required' })
      }
    } catch (error) {
      setToast({ type: 'error', message: 'Login failed' })
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gradient-to-br from-green-50 to-blue-50 flex items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-4xl font-bold text-green-600 mb-2">MaSecure</h1>
          <p className="text-gray-600">Admin Dashboard</p>
        </div>

        {toast && (
          <Toast
            message={toast.message}
            type={toast.type}
            onClose={() => setToast(null)}
          />
        )}

        <form onSubmit={handleSubmit}>
          <Input
            label="Email"
            type="email"
            name="email"
            value={formData.email}
            onChange={handleChange}
            placeholder="coordinatrice@example.com"
            required
          />

          <Input
            label="Password"
            type="password"
            name="password"
            value={formData.password}
            onChange={handleChange}
            placeholder="••••••••"
            required
          />

          <Button
            type="submit"
            variant="primary"
            disabled={loading}
            className="w-full"
          >
            {loading ? '🔄 Logging in...' : '✅ Login'}
          </Button>
        </form>

        <div className="mt-6 pt-6 border-t border-gray-200">
          <p className="text-xs text-gray-600 text-center">
            💡 Demo credentials:
          </p>
          <p className="text-xs text-gray-600 text-center font-mono mt-2">
            Email: test@example.com<br />
            Password: any
          </p>
        </div>
      </Card>
    </div>
  )
}
