package recent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/recent"
)

func TestHackerNews_Fetch(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		json.NewEncoder(w).Encode(map[string]any{
			"hits": []map[string]any{
				{"objectID": "1", "title": "Story with link", "url": "https://example.com/a", "points": 120, "num_comments": 40, "created_at_i": 1789900000, "story_text": ""},
				{"objectID": "2", "title": "Ask HN: text only", "url": "", "points": 5, "num_comments": 2, "created_at_i": 1789800000, "story_text": "<p>Some <b>html</b> body</p>"},
			},
		})
	}))
	defer server.Close()

	src := recent.NewHackerNews().WithBaseURL(server.URL)
	since := time.Unix(1789700000, 0)
	items, err := src.Fetch(context.Background(), "claude code", since, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Name() != "hackernews" {
		t.Errorf("name = %q", src.Name())
	}
	if gotQuery.Get("query") != "claude code" || gotQuery.Get("tags") != "story" {
		t.Errorf("query params = %v", gotQuery)
	}
	if gotQuery.Get("numericFilters") != "created_at_i>1789700000" {
		t.Errorf("numericFilters = %q", gotQuery.Get("numericFilters"))
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].URL != "https://example.com/a" || items[0].Score != 120 || items[0].Comments != 40 {
		t.Errorf("item 0 = %+v", items[0])
	}
	if items[0].Published.Unix() != 1789900000 {
		t.Errorf("published = %v", items[0].Published)
	}
	// Text-only stories link to the HN thread and carry the body without tags.
	if items[1].URL != "https://news.ycombinator.com/item?id=2" {
		t.Errorf("item 1 url = %q", items[1].URL)
	}
	if items[1].Snippet != "Some html body" {
		t.Errorf("item 1 snippet = %q", items[1].Snippet)
	}
}

func TestHackerNews_FetchHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	src := recent.NewHackerNews().WithBaseURL(server.URL)
	if _, err := src.Fetch(context.Background(), "q", time.Now(), 5); err == nil {
		t.Fatal("expected error on 502")
	}
}
