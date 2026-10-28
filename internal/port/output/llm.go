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
	Content string
	// FinishReason is the provider's reason for stopping: "stop" when the model
	// finished, "length" when it ran into the output cap. A report cut off by
	// the cap must never be mistaken for a complete one.
	FinishReason string
	ToolCalls    []entity.ToolCall
	// ToolTrace is what the tools actually returned during this turn. It is
	// kept so the facts a document read produced outlive the turn instead of
	// being reduced to the model's own prose.
	ToolTrace    []entity.ToolCall
	InputTokens  int
	OutputTokens int
	Model        string
}

type LLMProvider interface {
	Chat(ctx context.Context, req LLMRequest) (*LLMResponse, error)
	ChatStream(ctx context.Context, req LLMRequest, onChunk func(chunk string)) (*LLMResponse, error)
}
