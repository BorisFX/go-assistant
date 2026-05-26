package output

import (
	"context"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type LLMMessage struct {
	Role       entity.Role
	Content    string
	Images     []ImageContent // base64 encoded images
	ToolCalls  []entity.ToolCall
	ToolCallID string
}

type ImageContent struct {
	Base64   string // base64 encoded image data
	MimeType string // "image/jpeg", "image/png"
}

type LLMRequest struct {
	Messages    []LLMMessage
	Tools       []entity.ToolDefinition
	ToolNames   []entity.ToolSummary
	MaxTokens   int
	Temperature float64
	Model       string // override model (e.g. for vision use gpt-4o-mini)
}

type LLMResponse struct {
	Content      string
	ToolCalls    []entity.ToolCall
	InputTokens  int
	OutputTokens int
	Model        string
}

type LLMProvider interface {
	Chat(ctx context.Context, req LLMRequest) (*LLMResponse, error)
	ChatStream(ctx context.Context, req LLMRequest, onChunk func(chunk string)) (*LLMResponse, error)
}
