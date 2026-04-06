/**
 * Data adapters to normalize API responses
 * Convert backend data structure to frontend expectations
 */

// Mock data fallback
export const MOCK_GROUP_DATA = {
  group_id: 'group-001',
  name: 'Les Mamans du Quartier',
  balance: 150000,
  members_count: 8,
  active_cycles: 1,
  last_update: new Date().toISOString(),
}

export const MOCK_MEMBERS = [
  { id: 'member-001', name: 'Fato Diallo', email: 'fato@example.com', balance: 50000, status: 'active' },
  { id: 'member-002', name: 'Aissa Cissé', email: 'aissa@example.com', balance: 75000, status: 'active' },
  { id: 'member-003', name: 'Mariam Traoré', email: 'mariam@example.com', balance: 25000, status: 'active' },
]

export const MOCK_LEDGER = [
  { id: 'tx-001', date: new Date().toISOString(), from_member: 'Fato Diallo', to_member: 'Group Fund', amount: 50000, transaction_type: 'contribution', status: 'completed' },
  { id: 'tx-002', date: new Date().toISOString(), from_member: 'Group Fund', to_member: 'Aissa Cissé', amount: 200000, transaction_type: 'payout', status: 'completed' },
]

export function adaptGroupData(data) {
  // If API failed or no data, use mock
  const source = data || MOCK_GROUP_DATA
  
  return {
    groupID: source.group_id || source.groupID || source.id,
    name: source.name || 'Group Name',
    balance: typeof source.balance === 'number' 
      ? `${source.balance.toLocaleString()} FCFA` 
      : source.balance || '0 FCFA',
    membersCount: source.members_count || source.membersCount || 0,
    activeCycles: source.active_cycles || source.activeCycles || 0,
    lastUpdate: source.last_update || source.lastUpdate || new Date().toISOString(),
  }
}

export function adaptMembersData(data) {
  // If API failed or no data, use mock
  const source = Array.isArray(data) ? data : MOCK_MEMBERS
  
  return source.map(member => ({
    id: member.id || member.member_id,
    name: member.name || 'Unknown',
    email: member.email,
    phone: member.phone,
    balance: typeof member.balance === 'number'
      ? `${member.balance.toLocaleString()} FCFA`
      : member.balance || '0 FCFA',
    status: member.status || 'active',
  }))
}

export function adaptCyclesData(data) {
  const source = Array.isArray(data) ? data : []
  
  return source.map(cycle => ({
    id: cycle.id || cycle.cycle_id,
    startDate: cycle.start_date || cycle.startDate,
    endDate: cycle.end_date || cycle.endDate,
    targetAmount: cycle.target_amount || cycle.targetAmount || 0,
    status: cycle.status || 'active',
    description: cycle.description || '',
  }))
}

export function adaptLedgerData(data) {
  const source = Array.isArray(data) ? data : MOCK_LEDGER
  
  return source.map(tx => ({
    id: tx.id || tx.transaction_id,
    date: tx.date || tx.created_at,
    from: tx.from_member || tx.from || 'Unknown',
    to: tx.to_member || tx.to || 'Unknown',
    amount: typeof tx.amount === 'number'
      ? tx.amount.toLocaleString()
      : tx.amount || '0',
    type: tx.transaction_type || tx.type || 'transfer',
    status: tx.status || 'completed',
    cycleID: tx.cycle_id || tx.cycleID,
  }))
}
