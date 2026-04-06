import { useState, useEffect } from 'react'
import { useFetch } from '../hooks/useFetch'
import { GroupStats, MembersTable } from '../components/dashboard/Dashboard'
import { API_ENDPOINTS } from '../services/api'
import { adaptGroupData, adaptMembersData } from '../utils/adapters'
import { Card, Button } from '../components/common/Common'

export default function DashboardPage() {
  const { data: rawGroupData, isLoading: groupLoading } = useFetch(API_ENDPOINTS.GROUP)
  const { data: rawMembers, isLoading: membersLoading } = useFetch(API_ENDPOINTS.MEMBERS)
  
  // Adapt raw data from backend to frontend format
  const groupData = adaptGroupData(rawGroupData)
  const members = adaptMembersData(rawMembers)

  return (
    <div>
      <div className="flex justify-between items-center mb-8">
        <h1 className="text-3xl font-bold">Dashboard</h1>
        <div className="flex gap-2">
          <Button variant="primary">+ New Cycle</Button>
          <Button variant="secondary">📥 Export</Button>
        </div>
      </div>

      <GroupStats groupData={groupData} loading={groupLoading} />
      <MembersTable members={members} loading={membersLoading} />

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mt-8">
        <Card>
          <h3 className="font-semibold mb-4">📊 Quick Stats</h3>
          <ul className="space-y-2 text-sm">
            <li>✅ System: Operational</li>
            <li>✅ Database: Connected</li>
            <li>✅ API: Responding</li>
            <li>✅ Last Sync: 2 min ago</li>
          </ul>
        </Card>
        <Card>
          <h3 className="font-semibold mb-4">📢 Announcements</h3>
          <ul className="space-y-2 text-sm">
            <li>• Cycle 2 ends on April 15</li>
            <li>• New member request pending</li>
            <li>• System maintenance tomorrow 2-4 AM</li>
          </ul>
        </Card>
      </div>
    </div>
  )
}
