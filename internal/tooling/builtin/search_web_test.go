package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type mockSearchProvider struct{}

func (m *mockSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]output.SearchResult, error) {
	return []output.SearchResult{
		{Title: "Result 1", URL: "https://example.com/1", Content: "content 1"},
		{Title: "Result 2", URL: "https://example.com/2", Content: "content 2"},
	}, nil
}

func TestSearchWebTool_Metadata(t *testing.T) {
	tool := builtin.NewSearchWeb(&mockSearchProvider{})

	if tool.Name() != "search_web" {
		t.Errorf("expected name search_web, got %s", tool.Name())
	}
	if tool.Category() != "search" {
		t.Errorf("expected category search, got %s", tool.Category())
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestSearchWebTool_Execute(t *testing.T) {
	tool := builtin.NewSearchWeb(&mockSearchProvider{})

	params := json.RawMessage(`{"query": "Go developer Cambodia", "max_results": 5}`)
	result, err := tool.Execute(context.Background(), params)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []output.SearchResult
	if err := json.Unmarshal(result, &results); err != nil {
		t.Fatalf("invalid result: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}
