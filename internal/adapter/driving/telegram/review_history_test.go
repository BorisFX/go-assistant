package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type fakeMessages struct {
	conv  *entity.Conversation
	saved []*entity.Message
	list  []*entity.Message
}

func (f *fakeMessages) SaveMessage(_ context.Context, m *entity.Message) error {
	f.saved = append(f.saved, m)
	return nil
}
func (f *fakeMessages) GetConversation(_ context.Context, _ uuid.UUID) (*entity.Conversation, error) {
	return f.conv, nil
}
func (f *fakeMessages) GetOrCreateConversation(_ context.Context, _ string) (*entity.Conversation, error) {
	return f.conv, nil
}
func (f *fakeMessages) ListMessages(_ context.Context, _ uuid.UUID, _ int) ([]*entity.Message, error) {
	return f.list, nil
}
func (f *fakeMessages) ListConversations(_ context.Context, _, _ int) ([]*entity.Conversation, error) {
	return nil, nil
}

func TestReviewHistoryContextAndRemember(t *testing.T) {
	conv := &entity.Conversation{ID: uuid.New()}
	repo := &fakeMessages{conv: conv, list: []*entity.Message{
		{Role: entity.RoleUser, Content: "по testHotel вчера пришла приостановка"},
		{Role: entity.RoleTool, Content: "{json}"},
		{Role: entity.RoleAssistant, Content: "понял, смотрю"},
	}}
	h := newReviewHistory(repo)

	got := h.Context(context.Background(), "telegram:dm:1")
	if !strings.Contains(got, "приостановка") || !strings.Contains(got, "смотрю") || strings.Contains(got, "{json}") {
		t.Fatalf("context = %q", got)
	}

	h.Remember(context.Background(), "telegram:dm:1", "разбери папку testHotel", "ЗАКЛЮЧЕНИЕ …")
	if len(repo.saved) != 2 || repo.saved[0].Role != entity.RoleUser || repo.saved[1].Role != entity.RoleAssistant {
		t.Fatalf("review must land in history as a user/assistant pair: %+v", repo.saved)
	}
	if repo.saved[1].ConversationID != conv.ID || !strings.Contains(repo.saved[1].Content, "ЗАКЛЮЧЕНИЕ") {
		t.Fatalf("assistant turn = %+v", repo.saved[1])
	}
}

func TestReviewHistoryNilRepoIsQuiet(t *testing.T) {
	var h *reviewHistory
	if h.Context(context.Background(), "x") != "" {
		t.Fatal("nil history must yield no context")
	}
	h.Remember(context.Background(), "x", "a", "b") // must not panic
}
