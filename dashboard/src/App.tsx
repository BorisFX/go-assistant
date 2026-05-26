import { Routes, Route, Link, useLocation } from 'react-router-dom'
import ChatPage from './pages/ChatPage'
import ActivityPage from './pages/ActivityPage'
import SettingsPage from './pages/SettingsPage'
import MemoryPage from './pages/MemoryPage'

function Nav() {
  const location = useLocation()
  const links = [
    { to: '/', label: 'Chat' },
    { to: '/activity', label: 'Activity' },
    { to: '/memory', label: 'Memory' },
    { to: '/settings', label: 'Settings' },
  ]

  return (
    <nav className="bg-gray-900 text-white px-6 py-3 flex gap-6 items-center">
      <span className="font-bold text-lg mr-4">Go Assistant</span>
      {links.map(link => (
        <Link
          key={link.to}
          to={link.to}
          className={`hover:text-blue-400 ${location.pathname === link.to ? 'text-blue-400' : 'text-gray-300'}`}
        >
          {link.label}
        </Link>
      ))}
    </nav>
  )
}

export default function App() {
  return (
    <div className="min-h-screen bg-gray-950 text-white">
      <Nav />
      <main className="p-6 max-w-6xl mx-auto">
        <Routes>
          <Route path="/" element={<ChatPage />} />
          <Route path="/activity" element={<ActivityPage />} />
          <Route path="/memory" element={<MemoryPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </main>
    </div>
  )
}
