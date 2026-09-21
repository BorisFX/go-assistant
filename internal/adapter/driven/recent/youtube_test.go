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

func TestYouTube_Fetch(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": map[string]any{"videoId": "abc123"}, "snippet": map[string]any{
					"title": "Claude Code review", "description": "A video", "publishedAt": "2026-09-19T10:00:00Z", "channelTitle": "DevTube"}},
			},
		})
	}))
	defer server.Close()

	src := recent.NewYouTube("yt-key").WithBaseURL(server.URL)
	since := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	items, err := src.Fetch(context.Background(), "claude code", since, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Name() != "youtube" {
		t.Errorf("name = %q", src.Name())
	}
	if gotQuery.Get("key") != "yt-key" || gotQuery.Get("q") != "claude code" || gotQuery.Get("type") != "video" {
		t.Errorf("query = %v", gotQuery)
	}
	if gotQuery.Get("publishedAfter") != "2026-09-14T00:00:00Z" {
		t.Errorf("publishedAfter = %q", gotQuery.Get("publishedAfter"))
	}
	if gotQuery.Get("maxResults") != "5" {
		t.Errorf("maxResults = %q", gotQuery.Get("maxResults"))
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].URL != "https://www.youtube.com/watch?v=abc123" || items[0].Extra != "DevTube" {
		t.Errorf("item = %+v", items[0])
	}
	if items[0].Published.Format(time.RFC3339) != "2026-09-19T10:00:00Z" {
		t.Errorf("published = %v", items[0].Published)
	}
}

func TestYouTube_NoKey(t *testing.T) {
	if _, err := recent.NewYouTube("").Fetch(context.Background(), "q", time.Now(), 5); err == nil {
		t.Fatal("expected error without api key")
	}
}
