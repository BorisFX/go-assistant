package entity

import (
	"time"

	"github.com/google/uuid"
)

type MemoryType string

const (
	MemoryFact    MemoryType = "fact"
	MemorySummary MemoryType = "summary"
	MemoryEvent   MemoryType = "event"
)

type Memory struct {
	ID        uuid.UUID
	Type      MemoryType
	Content   string
	Tags      []string
	Embedding []float32
	Source    string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

func NewMemory(memType MemoryType, content, source string, tags []string) *Memory {
	return &Memory{
		ID:        uuid.New(),
		Type:      memType,
		Content:   content,
		Tags:      tags,
		Source:    source,
		CreatedAt: time.Now(),
	}
}
