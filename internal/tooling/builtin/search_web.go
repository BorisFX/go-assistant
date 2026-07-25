package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type SearchWeb struct {
	provider output.SearchProvider
}

func NewSearchWeb(provider output.SearchProvider) *SearchWeb {
	return &SearchWeb{provider: provider}
}

func (s *SearchWeb) Name() string { return "search_web" }
func (s *SearchWeb) Description() string {
	return "Search the internet for information. Pass a short keyword query (2-6 words), " +
		"not a full sentence — search engines match keywords, so a conversational phrase " +
		"returns irrelevant results. Keep the query in the same language as the topic. " +
		"For current events or 'today' questions, set topic=\"news\" and a time_range " +
		"(e.g. \"week\") so results are recent — otherwise old archive pages can surface. " +
		"Each result may include published_date; prefer the most recent ones."
}
func (s *SearchWeb) Category() string { return "search" }

func (s *SearchWeb) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Concise keyword search query (2-6 words), not a full sentence. Good: \"bitcoin price today\", \"новости Камбоджа виза\". Bad: \"what is the current price of bitcoin right now\"."
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of results to return",
				"default": 5
			},
			"topic": {
				"type": "string",
				"enum": ["general", "news"],
				"description": "Use \"news\" for current events / recent developments so results come from dated news sources. Default \"general\".",
				"default": "general"
			},
			"time_range": {
				"type": "string",
				"enum": ["day", "week", "month", "year"],
				"description": "Restrict results by recency. Set this for anything time-sensitive (\"today\", \"latest\", \"2026\") — e.g. \"day\" or \"week\". Omit for evergreen facts."
			}
		},
		"required": ["query"]
	}`)
}

type searchWebParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
	Topic      string `json:"topic"`
	TimeRange  string `json:"time_range"`
}

func (s *SearchWeb) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p searchWebParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	if p.MaxResults <= 0 {
		p.MaxResults = 5
	}

	results, err := s.provider.Search(ctx, p.Query, output.SearchOptions{
		MaxResults: p.MaxResults,
		Topic:      p.Topic,
		TimeRange:  p.TimeRange,
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return json.Marshal(results)
}
