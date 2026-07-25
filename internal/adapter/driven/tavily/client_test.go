package tavily_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/tavily"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func TestClient_Search(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Fresh news", "url": "https://example.com/1", "content": "today's story", "published_date": "2026-07-24"},
				{"title": "Older", "url": "https://example.com/2", "content": "context", "published_date": "2026-07-20"},
			},
		})
	}))
	defer server.Close()

	client := tavily.New("tvly-test-key", server.URL)
	results, err := client.Search(context.Background(), "prince news", output.SearchOptions{
		MaxResults: 5,
		TimeRange:  "week",
		Topic:      "news",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer tvly-test-key" {
		t.Errorf("expected bearer auth, got %q", gotAuth)
	}
	if gotBody["topic"] != "news" {
		t.Errorf("expected topic=news, got %v", gotBody["topic"])
	}
	if gotBody["time_range"] != "week" {
		t.Errorf("expected time_range=week, got %v", gotBody["time_range"])
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].PublishedDate != "2026-07-24" {
		t.Errorf("expected published_date carried through, got %q", results[0].PublishedDate)
	}
}

// A bad time_range must be dropped, not forwarded — Tavily rejects unknown
// values and that would kill an otherwise valid query.
func TestClient_Search_DropsBadTimeRange(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
	}))
	defer server.Close()

	client := tavily.New("tvly-test-key", server.URL)
	_, err := client.Search(context.Background(), "q", output.SearchOptions{TimeRange: "yesterday"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := gotBody["time_range"]; present {
		t.Errorf("bad time_range should be omitted, body had %v", gotBody["time_range"])
	}
}

func TestClient_Search_NoKey(t *testing.T) {
	client := tavily.New("", "")
	if _, err := client.Search(context.Background(), "q", output.SearchOptions{}); err == nil {
		t.Fatal("expected error when api key missing")
	}
}
