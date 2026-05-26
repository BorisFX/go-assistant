package event

import (
	"time"

	"github.com/google/uuid"
)

type Event interface {
	EventName() string
	OccurredAt() time.Time
}

type MessageReceived struct {
	MessageID      uuid.UUID
	ConversationID uuid.UUID
	Content        string
	SessionID      string
	Timestamp      time.Time
}

func (e MessageReceived) EventName() string     { return "message.received" }
func (e MessageReceived) OccurredAt() time.Time { return e.Timestamp }

type ToolExecuted struct {
	ToolName   string
	DurationMs int64
	Success    bool
	SessionID  string
	Timestamp  time.Time
}

func (e ToolExecuted) EventName() string     { return "tool.executed" }
func (e ToolExecuted) OccurredAt() time.Time { return e.Timestamp }
