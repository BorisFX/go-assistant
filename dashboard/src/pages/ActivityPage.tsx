import { useState, useEffect } from 'react'
import { api, type Activity, type ActivityStats } from '../api/client'

export default function ActivityPage() {
  const [activities, setActivities] = useState<Activity[]>([])
  const [stats, setStats] = useState<ActivityStats | null>(null)

  useEffect(() => {
    api.activity().then(setActivities).catch(console.error)
    api.activityStats().then(setStats).catch(console.error)
  }, [])

  return (
    <div>
      {stats && (
        <div className="grid grid-cols-2 gap-4 mb-6">
          <div className="bg-gray-900 rounded-lg p-4">
            <div className="text-sm text-gray-400">Today</div>
            <div className="text-2xl font-bold">${stats.today_cost.toFixed(4)}</div>
          </div>
          <div className="bg-gray-900 rounded-lg p-4">
            <div className="text-sm text-gray-400">This Month</div>
            <div className="text-2xl font-bold">${stats.month_cost.toFixed(4)}</div>
          </div>
        </div>
      )}

      <div className="bg-gray-900 rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-800">
            <tr>
              <th className="text-left p-3">Time</th>
              <th className="text-left p-3">Type</th>
              <th className="text-left p-3">Name</th>
              <th className="text-right p-3">Tokens</th>
              <th className="text-right p-3">Cost</th>
              <th className="text-right p-3">Duration</th>
            </tr>
          </thead>
          <tbody>
            {activities?.map(a => (
              <tr key={a.ID} className="border-t border-gray-800 hover:bg-gray-800/50">
                <td className="p-3 text-gray-400">{new Date(a.CreatedAt).toLocaleTimeString()}</td>
                <td className="p-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${a.Type === 'llm_call' ? 'bg-purple-900 text-purple-300' : 'bg-green-900 text-green-300'}`}>
                    {a.Type}
                  </span>
                </td>
                <td className="p-3">{a.Name}</td>
                <td className="p-3 text-right text-gray-400">{a.InputTokens + a.OutputTokens}</td>
                <td className="p-3 text-right">${a.CostUSD.toFixed(4)}</td>
                <td className="p-3 text-right text-gray-400">{a.DurationMs}ms</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
