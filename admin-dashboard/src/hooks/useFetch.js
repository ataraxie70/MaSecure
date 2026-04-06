import { useQuery, useMutation } from '@tanstack/react-query'
import { apiCall } from '../services/api'

export function useFetch(url, options = {}) {
  return useQuery({
    queryKey: [url],
    queryFn: () => apiCall('GET', url),
    staleTime: 5 * 60 * 1000, // 5 minutes
    ...options,
  })
}

export function usePost(url) {
  return useMutation({
    mutationFn: (data) => apiCall('POST', url, data),
  })
}

export function usePut(url) {
  return useMutation({
    mutationFn: (data) => apiCall('PUT', url, data),
  })
}

export function useDelete(url) {
  return useMutation({
    mutationFn: () => apiCall('DELETE', url),
  })
}
