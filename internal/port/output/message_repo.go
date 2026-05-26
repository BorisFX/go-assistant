package output

import (
	"context"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type MessageRepository interface {
	SaveMessage(ctx context.Context, msg *entity.Message) error
	GetConversation(ctx context.Context, id uuid.UUID) (*entity.Conversation, error)
	GetOrCreateConversation(ctx context.Context, sessionID string) (*entity.Conversation, error)
	ListMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]*entity.Message, error)
	ListConversations(ctx context.Context, limit, offset int) ([]*entity.Conversation, error)
}
