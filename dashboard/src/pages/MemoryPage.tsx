import { useState, useEffect } from 'react'

interface Memory {
  ID: string
  Type: string
  Content: string
  Tags: string[]
  Source: string
  CreatedAt: string
}

const API_KEY = localStorage.getItem('api_key') || ''

async function fetchMemories(): Promise<Memory[]> {
  const res = await fetch('/api/memory?limit=100', {
    headers: { 'X-API-Key': API_KEY },
  })
  return res.json()
}

async function createMemory(content: string, tags: string[]): Promise<void> {
  await fetch('/api/memory', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-API-Key': API_KEY },
    body: JSON.stringify({ content, tags }),
  })
}

async function deleteMemory(id: string): Promise<void> {
  await fetch(`/api/memory/${id}`, {
    method: 'DELETE',
    headers: { 'X-API-Key': API_KEY },
  })
}

export default function MemoryPage() {
  const [memories, setMemories] = useState<Memory[]>([])
  const [newContent, setNewContent] = useState('')
  const [newTags, setNewTags] = useState('')

  const load = () => fetchMemories().then(setMemories).catch(console.error)

  useEffect(() => { load() }, [])

  const handleCreate = async () => {
    if (!newContent.trim()) return
    const tags = newTags.split(',').map(t => t.trim()).filter(Boolean)
    await createMemory(newContent, tags)
    setNewContent('')
    setNewTags('')
    load()
  }

  const handleDelete = async (id: string) => {
    await deleteMemory(id)
    load()
  }

  const facts = memories?.filter(m => m.Type === 'fact') || []
  const summaries = memories?.filter(m => m.Type === 'summary') || []
  const events = memories?.filter(m => m.Type === 'event') || []

  return (
    <div>
      <div className="bg-gray-900 rounded-lg p-4 mb-6">
        <h2 className="text-sm font-semibold text-gray-400 mb-2">Add Fact</h2>
        <input
          value={newContent}
          onChange={e => setNewContent(e.target.value)}
          placeholder="e.g., user prefers Go for backend"
          className="w-full bg-gray-800 rounded px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-500 mb-2"
        />
        <input
          value={newTags}
          onChange={e => setNewTags(e.target.value)}
          placeholder="Tags (comma-separated)"
          className="w-full bg-gray-800 rounded px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-500 mb-2"
        />
        <button onClick={handleCreate} className="bg-blue-600 hover:bg-blue-500 px-4 py-2 rounded text-sm">
          Add
        </button>
      </div>

      <Section title={`Facts (${facts.length})`} memories={facts} onDelete={handleDelete} />
      <Section title={`Summaries (${summaries.length})`} memories={summaries} onDelete={handleDelete} />
      <Section title={`Events (${events.length})`} memories={events} onDelete={handleDelete} />
    </div>
  )
}

function Section({ title, memories, onDelete }: { title: string; memories: Memory[]; onDelete: (id: string) => void }) {
  return (
    <div className="mb-6">
      <h2 className="text-lg font-semibold mb-2">{title}</h2>
      <div className="space-y-2">
        {memories.map(m => (
          <div key={m.ID} className="bg-gray-900 rounded-lg p-3 flex justify-between items-start">
            <div className="flex-1">
              <p className="text-sm">{m.Content}</p>
              <div className="flex gap-2 mt-1">
                {m.Tags?.map(tag => (
                  <span key={tag} className="text-xs bg-gray-800 text-gray-400 px-2 py-0.5 rounded">{tag}</span>
                ))}
                <span className="text-xs text-gray-500">{m.Source} &middot; {new Date(m.CreatedAt).toLocaleDateString()}</span>
              </div>
            </div>
            <button onClick={() => onDelete(m.ID)} className="text-red-500 hover:text-red-400 text-xs ml-2">Delete</button>
          </div>
        ))}
        {memories.length === 0 && <p className="text-gray-500 text-sm">No entries</p>}
      </div>
    </div>
  )
}
