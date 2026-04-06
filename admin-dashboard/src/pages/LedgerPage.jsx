import { useState } from 'react'
import { useFetch } from '../hooks/useFetch'
import { API_ENDPOINTS } from '../services/api'
import { adaptLedgerData } from '../utils/adapters'
import { Card, Input, Button } from '../components/common/Common'

export default function LedgerPage() {
  const { data: rawTransactions, isLoading } = useFetch(API_ENDPOINTS.LEDGER)
  const transactions = adaptLedgerData(rawTransactions)
  const [filterMonth, setFilterMonth] = useState('')
  const [filterMember, setFilterMember] = useState('')

  const filteredTransactions = transactions?.filter((tx) => {
    let match = true
    if (filterMonth) {
      const txMonth = new Date(tx.date).toISOString().slice(0, 7)
      match = match && txMonth === filterMonth
    }
    if (filterMember) {
      match = match && (tx.from?.toLowerCase().includes(filterMember.toLowerCase()) ||
                        tx.to?.toLowerCase().includes(filterMember.toLowerCase()))
    }
    return match
  }) || []

  const handleExportCSV = () => {
    if (!filteredTransactions.length) {
      alert('No transactions to export')
      return
    }

    const csv = [
      ['Date', 'From', 'To', 'Amount', 'Type', 'Status'],
      ...filteredTransactions.map((tx) => [
        new Date(tx.date).toLocaleDateString(),
        tx.from,
        tx.to,
        tx.amount,
        tx.type,
        tx.status,
      ]),
    ]
      .map((row) => row.join(','))
      .join('\n')

    const blob = new Blob([csv], { type: 'text/csv' })
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `ledger-${new Date().toISOString().split('T')[0]}.csv`
    a.click()
  }

  const handlePrint = () => {
    window.print()
  }

  return (
    <div>
      <h1 className="text-3xl font-bold mb-6">📖 Transaction Ledger</h1>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-6">
        <Input
          label="Filter by Month"
          type="month"
          value={filterMonth}
          onChange={(e) => setFilterMonth(e.target.value)}
        />
        <Input
          label="Filter by Member"
          type="text"
          placeholder="Name or ID..."
          value={filterMember}
          onChange={(e) => setFilterMember(e.target.value)}
        />
        <div className="flex gap-2 items-end">
          <Button variant="primary" onClick={handleExportCSV}>
            📥 Export CSV
          </Button>
          <Button variant="secondary" onClick={handlePrint}>
            🖨️ Print
          </Button>
        </div>
      </div>

      <Card>
        {isLoading ? (
          <div className="text-center py-8">Loading transactions...</div>
        ) : filteredTransactions.length === 0 ? (
          <div className="text-center py-8 text-gray-500">No transactions found</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-gray-50">
                  <th className="text-left py-3 px-4">Date</th>
                  <th className="text-left py-3 px-4">From</th>
                  <th className="text-left py-3 px-4">To</th>
                  <th className="text-right py-3 px-4">Amount</th>
                  <th className="text-left py-3 px-4">Type</th>
                  <th className="text-left py-3 px-4">Status</th>
                </tr>
              </thead>
              <tbody>
                {filteredTransactions.map((tx, index) => (
                  <tr key={index} className="table-row-hover border-b">
                    <td className="py-3 px-4">
                      {new Date(tx.date).toLocaleDateString('fr-FR')}
                    </td>
                    <td className="py-3 px-4">{tx.from}</td>
                    <td className="py-3 px-4">{tx.to}</td>
                    <td className="py-3 px-4 text-right font-semibold">
                      {tx.amount.toLocaleString()} FCFA
                    </td>
                    <td className="py-3 px-4">
                      <span className="text-xs bg-blue-100 text-blue-700 px-2 py-1 rounded">
                        {tx.type}
                      </span>
                    </td>
                    <td className="py-3 px-4">
                      <span className={`text-xs px-2 py-1 rounded ${
                        tx.status === 'completed'
                          ? 'bg-green-100 text-green-700'
                          : 'bg-yellow-100 text-yellow-700'
                      }`}>
                        {tx.status}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <div className="mt-6 text-sm text-gray-600 text-center print:hidden">
        💡 Tip: Use the filters to narrow down transactions, then export or print
      </div>
    </div>
  )
}
