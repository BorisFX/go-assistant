package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/search"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type stubProvider struct {
	results []output.SearchResult
	err     error
	calls   *int
}

func (s stubProvider) Search(ctx context.Context, query string, opts output.SearchOptions) ([]output.SearchResult, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.results, s.err
}

func TestChain_UsesPrimaryWhenItReturnsResults(t *testing.T) {
	fallbackCalls := 0
	chain := search.NewChain().
		Add("primary", stubProvider{results: []output.SearchResult{{Title: "from primary"}}}).
		Add("fallback", stubProvider{results: []output.SearchResult{{Title: "from fallback"}}, calls: &fallbackCalls})

	res, err := chain.Search(context.Background(), "q", output.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Title != "from primary" {
		t.Fatalf("expected primary result, got %+v", res)
	}
	if fallbackCalls != 0 {
		t.Errorf("fallback should not be called when primary succeeds")
	}
}

func TestChain_FallsBackOnError(t *testing.T) {
	chain := search.NewChain().
		Add("primary", stubProvider{err: errors.New("boom")}).
		Add("fallback", stubProvider{results: []output.SearchResult{{Title: "rescued"}}})

	res, err := chain.Search(context.Background(), "q", output.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Title != "rescued" {
		t.Fatalf("expected fallback result, got %+v", res)
	}
}

func TestChain_FallsBackOnEmpty(t *testing.T) {
	chain := search.NewChain().
		Add("primary", stubProvider{results: []output.SearchResult{}}).
		Add("fallback", stubProvider{results: []output.SearchResult{{Title: "rescued"}}})

	res, err := chain.Search(context.Background(), "q", output.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Title != "rescued" {
		t.Fatalf("expected fallback result, got %+v", res)
	}
}

func TestChain_NilProviderIgnored(t *testing.T) {
	chain := search.NewChain().
		Add("primary", nil).
		Add("fallback", stubProvider{results: []output.SearchResult{{Title: "only one"}}})

	res, err := chain.Search(context.Background(), "q", output.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
}

func TestChain_AllFailReturnsError(t *testing.T) {
	chain := search.NewChain().
		Add("primary", stubProvider{err: errors.New("boom1")}).
		Add("fallback", stubProvider{err: errors.New("boom2")})

	if _, err := chain.Search(context.Background(), "q", output.SearchOptions{}); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}
