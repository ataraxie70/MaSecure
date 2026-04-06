import { Card } from '../common/Common'

export function GroupStats({ groupData, loading }) {
  if (loading) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-8">
        {[...Array(4)].map((_, i) => (
          <Card key={i} className="h-24 animate-pulse bg-gray-200" />
        ))}
      </div>
    )
  }

  if (!groupData) {
    return <div className="text-gray-500">No group data</div>
  }

  const stats = [
    {
      label: 'Total Balance',
      value: groupData.balance || '0 FCFA',
      icon: '💰',
    },
    {
      label: 'Members',
      value: groupData.membersCount || 0,
      icon: '👥',
    },
    {
      label: 'Active Cycles',
      value: groupData.activeCycles || 0,
      icon: '📅',
    },
    {
      label: 'Last Update',
      value: groupData.lastUpdate || 'N/A',
      icon: '🕐',
    },
  ]

  return (
    <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-8">
      {stats.map((stat, index) => (
        <Card key={index}>
          <div className="flex items-center justify-between">
            <div>
              <p className="text-gray-600 text-sm">{stat.label}</p>
              <p className="text-2xl font-bold text-gray-900">{stat.value}</p>
            </div>
            <span className="text-4xl">{stat.icon}</span>
          </div>
        </Card>
      ))}
    </div>
  )
}

export function MembersTable({ members, loading }) {
  if (loading) {
    return (
      <Card>
        <div className="animate-pulse space-y-3">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-8 bg-gray-200 rounded" />
          ))}
        </div>
      </Card>
    )
  }

  if (!members || members.length === 0) {
    return <Card>No members found</Card>
  }

  return (
    <Card>
      <h2 className="text-lg font-semibold mb-4">👥 Members</h2>
      <table className="w-full">
        <thead>
          <tr className="border-b">
            <th className="text-left py-2 px-3">Name</th>
            <th className="text-left py-2 px-3">Status</th>
            <th className="text-right py-2 px-3">Balance</th>
          </tr>
        </thead>
        <tbody>
          {members.map((member, index) => (
            <tr key={index} className="table-row-hover border-b">
              <td className="py-2 px-3">{member.name}</td>
              <td className="py-2 px-3">
                <span className={`px-2 py-1 rounded-full text-xs font-semibold ${
                  member.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-700'
                }`}>
                  {member.status || 'active'}
                </span>
              </td>
              <td className="py-2 px-3 text-right">{member.balance || '0 FCFA'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  )
}
