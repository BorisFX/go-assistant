package recent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/recent"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type fakeProvider struct {
	gotOpts output.SearchOptions
	results []output.SearchResult
	err     error
}

func (f *fakeProvider) Search(ctx context.Context, query string, opts output.SearchOptions) ([]output.SearchResult, error) {
	f.gotOpts = opts
	return f.results, f.err
}

func TestRedditViaSearch_Fetch(t *testing.T) {
	p := &fakeProvider{results: []output.SearchResult{
		{Title: "Thread", URL: "https://www.reddit.com/r/golang/comments/x/thread/", Content: "text", PublishedDate: "2026-09-18"},
		{Title: "Undated", URL: "https://www.reddit.com/r/golang/comments/y/", Content: "text"},
	}}
	src := recent.NewRedditViaSearch(p)
	items, err := src.Fetch(context.Background(), "go generics", time.Now().Add(-7*24*time.Hour), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Name() != "reddit" {
		t.Errorf("name = %q", src.Name())
	}
	if len(p.gotOpts.IncludeDomains) != 1 || p.gotOpts.IncludeDomains[0] != "reddit.com" {
		t.Errorf("include_domains = %v", p.gotOpts.IncludeDomains)
	}
	if p.gotOpts.TimeRange != "week" || p.gotOpts.MaxResults != 6 {
		t.Errorf("opts = %+v", p.gotOpts)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Published.Format("2006-01-02") != "2026-09-18" {
		t.Errorf("published = %v", items[0].Published)
	}
	if items[0].Extra != "r/golang" {
		t.Errorf("extra = %q", items[0].Extra)
	}
	if !items[1].Published.IsZero() {
		t.Errorf("undated result should have zero time, got %v", items[1].Published)
	}
}

func TestWithFallback(t *testing.T) {
	primary := &stubSource{name: "reddit", err: errors.New("upstream 502")}
	secondary := &stubSource{name: "reddit", items: []recent.Item{{Title: "from fallback"}}}
	src := recent.WithFallback(primary, secondary)

	items, err := src.Fetch(context.Background(), "q", time.Now(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Title != "from fallback" {
		t.Errorf("items = %+v", items)
	}

	// Empty primary result is a real answer, not a reason to fall back.
	primary.err = nil
	items, _ = src.Fetch(context.Background(), "q", time.Now(), 5)
	if len(items) != 0 {
		t.Errorf("expected primary's empty result, got %+v", items)
	}
}

type stubSource struct {
	name  string
	items []recent.Item
	err   error
}

func (s *stubSource) Name() string { return s.name }
func (s *stubSource) Fetch(context.Context, string, time.Time, int) ([]recent.Item, error) {
	return s.items, s.err
}
