import { useState } from 'react'

export default function SettingsPage() {
  const [apiKey, setApiKey] = useState(localStorage.getItem('api_key') || '')

  const save = () => {
    localStorage.setItem('api_key', apiKey)
    window.location.reload()
  }

  return (
    <div className="max-w-md">
      <h1 className="text-xl font-bold mb-4">Settings</h1>

      <div className="bg-gray-900 rounded-lg p-4">
        <label className="block text-sm text-gray-400 mb-1">API Key</label>
        <input
          type="password"
          value={apiKey}
          onChange={e => setApiKey(e.target.value)}
          className="w-full bg-gray-800 rounded px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="Enter your dashboard API key"
        />
        <button
          onClick={save}
          className="mt-3 bg-blue-600 hover:bg-blue-500 px-4 py-2 rounded text-sm"
        >
          Save
        </button>
      </div>
    </div>
  )
}
