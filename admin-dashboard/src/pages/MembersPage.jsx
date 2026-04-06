import { useState } from 'react'
import { useFetch } from '../hooks/useFetch'
import { API_ENDPOINTS } from '../services/api'
import { adaptMembersData } from '../utils/adapters'
import { Card, Input, Button } from '../components/common/Common'

export default function MembersPage() {
  const { data: rawMembers, isLoading } = useFetch(API_ENDPOINTS.MEMBERS)
  const members = adaptMembersData(rawMembers)
  const [searchTerm, setSearchTerm] = useState('')
  const [selectedMember, setSelectedMember] = useState(null)

  const filteredMembers = members?.filter((member) =>
    member.name?.toLowerCase().includes(searchTerm.toLowerCase())
  ) || []

  return (
    <div>
      <h1 className="text-3xl font-bold mb-6">👥 Members Management</h1>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        {/* Search & List */}
        <div className="md:col-span-2">
          <Card>
            <Input
              type="text"
              placeholder="Search members..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
            />

            {isLoading ? (
              <div className="text-center py-8">Loading...</div>
            ) : (
              <div className="space-y-2">
                {filteredMembers.map((member) => (
                  <div
                    key={member.id}
                    className="p-3 border rounded-lg hover:bg-blue-50 cursor-pointer transition-colors"
                    onClick={() => setSelectedMember(member)}
                  >
                    <div className="flex justify-between items-center">
                      <span className="font-medium">{member.name}</span>
                      <span className="text-sm text-gray-600">{member.balance || '0 FCFA'}</span>
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                      ID: {member.id}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>

        {/* Detail Panel */}
        <div className="md:col-span-1">
          {selectedMember ? (
            <Card>
              <h3 className="font-semibold mb-4">Member Details</h3>
              <div className="space-y-3 text-sm">
                <div>
                  <label className="text-gray-600">Name</label>
                  <p className="font-medium">{selectedMember.name}</p>
                </div>
                <div>
                  <label className="text-gray-600">ID</label>
                  <p className="font-medium">{selectedMember.id}</p>
                </div>
                <div>
                  <label className="text-gray-600">Balance</label>
                  <p className="font-medium text-green-600">{selectedMember.balance}</p>
                </div>
                <div>
                  <label className="text-gray-600">Status</label>
                  <p className="font-medium">{selectedMember.status || 'Active'}</p>
                </div>
                <div className="pt-4 space-y-2">
                  <Button variant="primary" className="w-full">Edit</Button>
                  <Button variant="secondary" className="w-full">Deactivate</Button>
                </div>
              </div>
            </Card>
          ) : (
            <Card>
              <p className="text-center text-gray-500">Select a member to view details</p>
            </Card>
          )}
        </div>
      </div>
    </div>
  )
}
