import { useState, useEffect } from 'react'
import { api, type Conversation, type Message } from '../api/client'

export default function ChatPage() {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [selectedConv, setSelectedConv] = useState<string | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    api.conversations().then(setConversations).catch(console.error)
  }, [])

  useEffect(() => {
    if (selectedConv) {
      api.messages(selectedConv).then(setMessages).catch(console.error)
    }
  }, [selectedConv])

  const sendMessage = async () => {
    if (!input.trim() || loading) return
    setLoading(true)

    try {
      const resp = await api.chat(input)
      setMessages(prev => [
        ...prev,
        { ID: crypto.randomUUID(), Role: 'user', Content: input, CreatedAt: new Date().toISOString() },
        { ID: crypto.randomUUID(), Role: 'assistant', Content: resp.Content, CreatedAt: new Date().toISOString() },
      ])
      setInput('')
      api.conversations().then(setConversations).catch(console.error)
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex gap-4 h-[calc(100vh-8rem)]">
      <div className="w-64 bg-gray-900 rounded-lg p-3 overflow-y-auto">
        <h2 className="text-sm font-semibold text-gray-400 mb-2">Conversations</h2>
        {conversations?.map(conv => (
          <button
            key={conv.ID}
            onClick={() => setSelectedConv(conv.ID)}
            className={`w-full text-left p-2 rounded text-sm mb-1 ${selectedConv === conv.ID ? 'bg-blue-900' : 'hover:bg-gray-800'}`}
          >
            <div className="truncate">{conv.SessionID}</div>
            <div className="text-xs text-gray-500">{new Date(conv.UpdatedAt).toLocaleString()}</div>
          </button>
        ))}
      </div>

      <div className="flex-1 flex flex-col bg-gray-900 rounded-lg">
        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {messages?.map(msg => (
            <div key={msg.ID} className={`flex ${msg.Role === 'user' ? 'justify-end' : 'justify-start'}`}>
              <div className={`max-w-[70%] rounded-lg p-3 text-sm ${msg.Role === 'user' ? 'bg-blue-700' : 'bg-gray-800'}`}>
                <pre className="whitespace-pre-wrap font-sans">{msg.Content}</pre>
                <div className="text-xs text-gray-500 mt-1">{new Date(msg.CreatedAt).toLocaleTimeString()}</div>
              </div>
            </div>
          ))}
        </div>

        <div className="p-3 border-t border-gray-800 flex gap-2">
          <input
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && sendMessage()}
            placeholder="Type a message..."
            className="flex-1 bg-gray-800 rounded px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-500"
            disabled={loading}
          />
          <button
            onClick={sendMessage}
            disabled={loading}
            className="bg-blue-600 hover:bg-blue-500 px-4 py-2 rounded text-sm disabled:opacity-50"
          >
            {loading ? '...' : 'Send'}
          </button>
        </div>
      </div>
    </div>
  )
}
