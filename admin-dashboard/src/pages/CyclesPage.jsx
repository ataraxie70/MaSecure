import { useState } from 'react'
import { usePost } from '../hooks/useFetch'
import { API_ENDPOINTS } from '../services/api'
import { Card, Input, Button, Toast } from '../components/common/Common'

export default function CyclesPage() {
  const [formData, setFormData] = useState({
    startDate: '',
    endDate: '',
    targetAmount: '',
    description: '',
  })
  const [toast, setToast] = useState(null)
  
  const createCycleMutation = usePost(API_ENDPOINTS.CYCLES)

  const handleChange = (e) => {
    setFormData({
      ...formData,
      [e.target.name]: e.target.value,
    })
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    try {
      await createCycleMutation.mutateAsync(formData)
      setToast({ type: 'success', message: 'Cycle created successfully!' })
      setFormData({ startDate: '', endDate: '', targetAmount: '', description: '' })
      setTimeout(() => setToast(null), 3000)
    } catch (error) {
      setToast({ type: 'error', message: 'Failed to create cycle' })
    }
  }

  return (
    <div>
      <h1 className="text-3xl font-bold mb-6">📅 Create New Cycle</h1>

      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Card>
          <h2 className="text-lg font-semibold mb-6">Cycle Details</h2>
          <form onSubmit={handleSubmit}>
            <Input
              label="Start Date"
              type="date"
              name="startDate"
              value={formData.startDate}
              onChange={handleChange}
              required
            />
            <Input
              label="End Date"
              type="date"
              name="endDate"
              value={formData.endDate}
              onChange={handleChange}
              required
            />
            <Input
              label="Target Amount (FCFA)"
              type="number"
              name="targetAmount"
              value={formData.targetAmount}
              onChange={handleChange}
              placeholder="100000"
              required
            />
            <div className="mb-4">
              <label className="block text-sm font-medium mb-1">Description</label>
              <textarea
                name="description"
                value={formData.description}
                onChange={handleChange}
                placeholder="Enter cycle description..."
                className="input-field w-full h-24 resize-none"
              />
            </div>
            <div className="flex gap-2">
              <Button
                type="submit"
                variant="primary"
                disabled={createCycleMutation.isPending}
              >
                {createCycleMutation.isPending ? 'Creating...' : '✅ Create Cycle'}
              </Button>
              <Button
                type="button"
                variant="secondary"
                onClick={() => setFormData({ startDate: '', endDate: '', targetAmount: '', description: '' })}
              >
                Clear
              </Button>
            </div>
          </form>
        </Card>

        <Card>
          <h2 className="text-lg font-semibold mb-6">📋 Guidelines</h2>
          <ul className="space-y-3 text-sm">
            <li>✅ Start date must be today or later</li>
            <li>✅ End date must be after start date</li>
            <li>✅ Target amount should be realistic</li>
            <li>✅ Description helps members understand the cycle</li>
            <li>✅ Once created, cycle cannot be deleted</li>
            <li>✅ Members will be notified via WhatsApp</li>
          </ul>
        </Card>
      </div>
    </div>
  )
}
