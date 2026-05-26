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

func (s *SearchWeb) Name() string        { return "search_web" }
func (s *SearchWeb) Description() string { return "Search the internet for information" }
func (s *SearchWeb) Category() string    { return "search" }

func (s *SearchWeb) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query"
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of results to return",
				"default": 5
			}
		},
		"required": ["query"]
	}`)
}

type searchWebParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func (s *SearchWeb) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p searchWebParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	if p.MaxResults <= 0 {
		p.MaxResults = 5
	}

	results, err := s.provider.Search(ctx, p.Query, p.MaxResults)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return json.Marshal(results)
}
