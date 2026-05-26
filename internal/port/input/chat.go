package input

import (
	"context"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type ChatRequest struct {
	SessionKey valueobject.SessionKey
	Content    string
	Images     []output.ImageContent // for multimodal (photos, schemas)
	ReplyToID  string
	OnProgress func(status string)
}

type ChatResponse struct {
	Content   string
	ToolCalls []entity.ToolCall
}

type ChatService interface {
	ProcessMessage(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
