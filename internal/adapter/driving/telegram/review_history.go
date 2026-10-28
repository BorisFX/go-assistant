package telegram

import (
	"context"
	"log/slog"

	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// reviewHistoryDepth is how many stored messages are read back; RenderContext
// trims further. Tool turns are stored too, so this is wider than what is shown.
const reviewHistoryDepth = 30

// reviewHistory joins the review pipeline to the chat's conversation store:
// a review reads the recent chat as context, and its conclusion is written
// back so the next chat question can refer to it. Both halves are optional —
// a nil history simply reviews the documents on their own.
type reviewHistory struct {
	repo output.MessageRepository
}

func newReviewHistory(repo output.MessageRepository) *reviewHistory {
	if repo == nil {
		return nil
	}
	return &reviewHistory{repo: repo}
}

// Context renders the recent chat of one session for the coordinator.
func (h *reviewHistory) Context(ctx context.Context, sessionID string) string {
	if h == nil {
		return ""
	}
	conv, err := h.repo.GetOrCreateConversation(ctx, sessionID)
	if err != nil {
		slog.Warn("legalreview: chat history unavailable", "error", err)
		return ""
	}
	msgs, err := h.repo.ListMessages(ctx, conv.ID, reviewHistoryDepth)
	if err != nil {
		slog.Warn("legalreview: chat history unavailable", "error", err)
		return ""
	}
	turns := make([]legalreview.ChatTurn, 0, len(msgs))
	for _, m := range msgs {
		turns = append(turns, legalreview.ChatTurn{Role: string(m.Role), Text: m.Content})
	}
	return legalreview.RenderContext(turns)
}

// Remember stores the review request and its conclusion as an ordinary
// user/assistant exchange, so chat after the review knows what was found.
func (h *reviewHistory) Remember(ctx context.Context, sessionID, ask, conclusion string) {
	if h == nil {
		return
	}
	conv, err := h.repo.GetOrCreateConversation(ctx, sessionID)
	if err != nil {
		slog.Warn("legalreview: cannot record review in chat", "error", err)
		return
	}
	for _, m := range []*entity.Message{
		entity.NewMessage(conv.ID, entity.RoleUser, ask),
		entity.NewMessage(conv.ID, entity.RoleAssistant, conclusion),
	} {
		if err := h.repo.SaveMessage(ctx, m); err != nil {
			slog.Warn("legalreview: cannot record review in chat", "error", err)
			return
		}
	}
}
