export default function Sidebar() {
  return (
    <aside className="hidden md:block w-64 bg-gray-900 text-white p-6">
      <div className="mb-8">
        <h2 className="text-xl font-bold text-green-400">Navigation</h2>
      </div>
      
      <nav className="space-y-3">
        <a href="/" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors">
          📊 Dashboard
        </a>
        <a href="/members" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors">
          👥 Members
        </a>
        <a href="/cycles" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors">
          📅 Cycles
        </a>
        <a href="/ledger" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors">
          📖 Ledger
        </a>
      </nav>

      <hr className="my-6 border-gray-700" />
      
      <nav className="space-y-3">
        <a href="#" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors text-sm">
          ⚙️ Settings
        </a>
        <a href="#" className="block px-4 py-2 rounded-lg hover:bg-gray-800 transition-colors text-sm">
          📞 Support
        </a>
      </nav>
    </aside>
  )
}
