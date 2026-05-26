package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type MessageRepo struct {
	db *DB
}

func NewMessageRepo(db *DB) *MessageRepo {
	return &MessageRepo{db: db}
}

func (r *MessageRepo) SaveMessage(ctx context.Context, msg *entity.Message) error {
	toolCallsJSON, err := json.Marshal(msg.ToolCalls)
	if err != nil {
		return fmt.Errorf("marshal tool calls: %w", err)
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, tool_calls, tool_result_for, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		msg.ID, msg.ConversationID, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolResultFor, msg.CreatedAt,
	)
	return err
}

func (r *MessageRepo) GetConversation(ctx context.Context, id uuid.UUID) (*entity.Conversation, error) {
	var conv entity.Conversation
	err := r.db.QueryRowContext(ctx,
		`SELECT id, session_id, title, created_at, updated_at FROM conversations WHERE id=$1`,
		id,
	).Scan(&conv.ID, &conv.SessionID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &conv, err
}

func (r *MessageRepo) GetOrCreateConversation(ctx context.Context, sessionID string) (*entity.Conversation, error) {
	var conv entity.Conversation
	err := r.db.QueryRowContext(ctx,
		`SELECT id, session_id, title, created_at, updated_at
		 FROM conversations
		 WHERE session_id=$1
		 ORDER BY updated_at DESC LIMIT 1`,
		sessionID,
	).Scan(&conv.ID, &conv.SessionID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)

	if err == sql.ErrNoRows {
		conv = *entity.NewConversation(sessionID)
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO conversations (id, session_id, title, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)`,
			conv.ID, conv.SessionID, conv.Title, conv.CreatedAt, conv.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
		return &conv, nil
	}

	return &conv, err
}

func (r *MessageRepo) ListMessages(ctx context.Context, conversationID uuid.UUID, limit int) ([]*entity.Message, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, tool_calls, tool_result_for, created_at
		 FROM messages
		 WHERE conversation_id=$1
		 ORDER BY created_at DESC LIMIT $2`,
		conversationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*entity.Message
	for rows.Next() {
		var msg entity.Message
		var toolCallsJSON []byte
		err := rows.Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &toolCallsJSON, &msg.ToolResultFor, &msg.CreatedAt)
		if err != nil {
			return nil, err
		}
		if toolCallsJSON != nil {
			json.Unmarshal(toolCallsJSON, &msg.ToolCalls)
		}
		messages = append(messages, &msg)
	}

	// Reverse to return chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

func (r *MessageRepo) ListConversations(ctx context.Context, limit, offset int) ([]*entity.Conversation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, session_id, title, created_at, updated_at
		 FROM conversations
		 ORDER BY updated_at DESC
		 LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []*entity.Conversation
	for rows.Next() {
		var conv entity.Conversation
		if err := rows.Scan(&conv.ID, &conv.SessionID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, &conv)
	}
	return convs, nil
}
