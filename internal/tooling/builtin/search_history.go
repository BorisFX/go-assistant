package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

// historySearcher is the slice of the message repository this tool needs.
type historySearcher interface {
	SearchContent(ctx context.Context, query string, limit int) ([]entity.Message, error)
}

const (
	historyDefaultLimit = 10
	historyMaxLimit     = 30
	historyExcerptRunes = 400
)

// SearchHistory looks up past conversation messages by substring. It replaces
// the old instruction that had the model run psql through bash with the
// database password in the system prompt.
type SearchHistory struct {
	repo historySearcher
}

func NewSearchHistory(repo historySearcher) *SearchHistory { return &SearchHistory{repo: repo} }

func (s *SearchHistory) Name() string { return "search_history" }

func (s *SearchHistory) Description() string {
	return "Search past conversation messages by a keyword or phrase (case-insensitive substring). Use it when the user refers to something discussed earlier"
}

func (s *SearchHistory) Category() string { return "memory" }

func (s *SearchHistory) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Keyword or phrase to look for in past messages"},
			"limit": {"type": "integer", "description": "Maximum messages to return, default 10, max 30"}
		},
		"required": ["query"]
	}`)
}

type searchHistoryParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type historyHit struct {
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	Excerpt   string    `json:"excerpt"`
}

func (s *SearchHistory) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p searchHistoryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	p.Query = strings.TrimSpace(p.Query)
	if p.Query == "" {
		return nil, fmt.Errorf("search_history: query is required")
	}
	if p.Limit <= 0 {
		p.Limit = historyDefaultLimit
	}
	if p.Limit > historyMaxLimit {
		p.Limit = historyMaxLimit
	}

	msgs, err := s.repo.SearchContent(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("search_history: %w", err)
	}

	hits := make([]historyHit, 0, len(msgs))
	for _, m := range msgs {
		hits = append(hits, historyHit{
			Role:      string(m.Role),
			CreatedAt: m.CreatedAt,
			Excerpt:   excerptAround(m.Content, p.Query, historyExcerptRunes),
		})
	}
	return json.Marshal(map[string]any{"query": p.Query, "messages": hits})
}

// excerptAround cuts a window of at most max runes around the first match, so
// a long report still shows the part that matched rather than its header.
func excerptAround(content, query string, max int) string {
	runes := []rune(content)
	if len(runes) <= max {
		return content
	}
	idx := strings.Index(strings.ToLower(content), strings.ToLower(query))
	start := 0
	if idx > 0 {
		start = len([]rune(content[:idx])) - max/4
		if start < 0 {
			start = 0
		}
	}
	end := start + max
	if end > len(runes) {
		end = len(runes)
		start = end - max
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
