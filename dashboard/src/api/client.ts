const API_KEY = localStorage.getItem('api_key') || ''

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
      ...options?.headers,
    },
  })

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }

  return res.json()
}

export interface Conversation {
  ID: string
  SessionID: string
  Title: string
  CreatedAt: string
  UpdatedAt: string
}

export interface Message {
  ID: string
  Role: string
  Content: string
  CreatedAt: string
}

export interface Activity {
  ID: string
  Type: string
  Name: string
  InputTokens: number
  OutputTokens: number
  CostUSD: number
  DurationMs: number
  CreatedAt: string
}

export interface ActivityStats {
  today_cost: number
  month_cost: number
}

export interface ChatResponse {
  Content: string
}

export const api = {
  health: () => request<{ status: string }>('/api/health'),
  conversations: (limit = 20) => request<Conversation[]>(`/api/conversations?limit=${limit}`),
  messages: (convId: string, limit = 50) => request<Message[]>(`/api/conversations/${convId}/messages?limit=${limit}`),
  chat: (message: string) => request<ChatResponse>('/api/chat', {
    method: 'POST',
    body: JSON.stringify({ message }),
  }),
  activity: (limit = 50) => request<Activity[]>(`/api/activity?limit=${limit}`),
  activityStats: () => request<ActivityStats>('/api/activity/stats'),
}
