package entity

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Role           Role
	Content        string
	ToolCalls      []ToolCall
	ToolResultFor  string
	CreatedAt      time.Time
}

type ToolCall struct {
	ID     string
	Name   string
	Args   string
	Result string
}

type Conversation struct {
	ID        uuid.UUID
	SessionID string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewConversation(sessionID string) *Conversation {
	now := time.Now()
	return &Conversation{
		ID:        uuid.New(),
		SessionID: sessionID,
		Title:     "",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func NewMessage(conversationID uuid.UUID, role Role, content string) *Message {
	return &Message{
		ID:             uuid.New(),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      time.Now(),
	}
}
