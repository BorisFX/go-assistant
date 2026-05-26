package entity

import (
	"time"

	"github.com/google/uuid"
)

type ActivityType string

const (
	ActivityLLMCall  ActivityType = "llm_call"
	ActivityToolCall ActivityType = "tool_call"
)

type Activity struct {
	ID             uuid.UUID
	Type           ActivityType
	Name           string
	InputTokens    int
	OutputTokens   int
	CostUSD        float64
	DurationMs     int64
	SessionID      string
	ConversationID uuid.UUID
	Metadata       string
	CreatedAt      time.Time
}

func NewActivity(actType ActivityType, name string, sessionID string, convID uuid.UUID) *Activity {
	return &Activity{
		ID:             uuid.New(),
		Type:           actType,
		Name:           name,
		SessionID:      sessionID,
		ConversationID: convID,
		CreatedAt:      time.Now(),
	}
}
