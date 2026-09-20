package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type fakeHistory struct {
	gotQuery string
	gotLimit int
	out      []entity.Message
}

func (f *fakeHistory) SearchContent(_ context.Context, q string, limit int) ([]entity.Message, error) {
	f.gotQuery, f.gotLimit = q, limit
	return f.out, nil
}

func TestSearchHistoryReturnsExcerpts(t *testing.T) {
	long := strings.Repeat("вода ", 200) + "ключ: приостановка по ст. 26" + strings.Repeat(" ещё", 200)
	repo := &fakeHistory{out: []entity.Message{
		{Role: entity.RoleUser, Content: long, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}}
	tool := NewSearchHistory(repo)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"приостановка"}`))
	if err != nil {
		t.Fatal(err)
	}
	if repo.gotQuery != "приостановка" || repo.gotLimit != historyDefaultLimit {
		t.Fatalf("repo got %q/%d", repo.gotQuery, repo.gotLimit)
	}
	var res struct {
		Messages []historyHit `json:"messages"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Role != "user" {
		t.Fatalf("unexpected result: %+v", res)
	}
	ex := res.Messages[0].Excerpt
	if !strings.Contains(ex, "приостановка") || len([]rune(ex)) > historyExcerptRunes+2 {
		t.Fatalf("excerpt should be windowed around the match: %q", ex)
	}
}

func TestSearchHistoryValidation(t *testing.T) {
	repo := &fakeHistory{}
	tool := NewSearchHistory(repo)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"  "}`)); err == nil {
		t.Fatal("empty query must be rejected")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"x","limit":500}`)); err != nil {
		t.Fatal(err)
	}
	if repo.gotLimit != historyMaxLimit {
		t.Fatalf("limit should be capped at %d, got %d", historyMaxLimit, repo.gotLimit)
	}
}
