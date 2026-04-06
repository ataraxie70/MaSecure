import axios from 'axios'

// In development: Vite proxy handles /api/* requests
// In production: VITE_API_URL should be the actual API domain
const isProduction = import.meta.env.PROD
const API_BASE_URL = isProduction 
  ? import.meta.env.VITE_API_URL || 'https://api.masecure.app'
  : ''  // Development: use proxy (base URL empty, paths start with /api)

const api = axios.create({
  baseURL: API_BASE_URL,
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Add token to requests
api.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('auth_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// Handle responses
api.interceptors.response.use(
  (response) => response.data,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('auth_token')
      window.location.href = '/login'
    }
    return Promise.reject(error.response?.data || error.message)
  }
)

export async function apiCall(method, url, data = null) {
  try {
    const response = await api({
      method,
      url,
      data,
    })
    return response
  } catch (error) {
    throw error
  }
}

export const API_ENDPOINTS = {
  GROUP: '/api/v1/group',
  MEMBERS: '/api/v1/members',
  CYCLES: '/api/v1/cycles',
  LEDGER: '/api/v1/ledger',
  TRANSACTIONS: '/api/v1/transactions',
}

export default api
