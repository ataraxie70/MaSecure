import api from './api'

export async function login(email, password) {
  const response = await api.post('/api/v1/auth/login', { email, password })
  if (response.token) {
    localStorage.setItem('auth_token', response.token)
  }
  return response
}

export async function logout() {
  localStorage.removeItem('auth_token')
  localStorage.removeItem('user_data')
}

export function getStoredToken() {
  return localStorage.getItem('auth_token')
}

export function isAuthenticated() {
  return !!getStoredToken()
}
