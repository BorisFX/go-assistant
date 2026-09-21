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

func TestReddit_Fetch(t *testing.T) {
	var gotAuth string
	var gotQuery url.Values
	now := time.Now()
	fresh := now.Add(-2 * 24 * time.Hour).Unix()
	stale := now.Add(-40 * 24 * time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query()
		// redditapis returns numbers as strings; the client must accept both.
		json.NewEncoder(w).Encode(map[string]any{
			"posts": []map[string]any{
				{"title": "Fresh post", "url": "https://reddit.com/r/golang/comments/1/fresh", "text": "body", "subreddit": "golang", "upvotes": "42", "comments": "7", "created_utc": fresh},
				{"title": "Stale post", "url": "https://reddit.com/r/golang/comments/2/stale", "text": "", "subreddit": "golang", "upvotes": 999, "comments": 1, "created_utc": stale},
			},
			"after": "cursor",
		})
	}))
	defer server.Close()

	src := recent.NewReddit("test-key").WithBaseURL(server.URL)
	items, err := src.Fetch(context.Background(), "go generics", now.Add(-7*24*time.Hour), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Name() != "reddit" {
		t.Errorf("name = %q", src.Name())
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotQuery.Get("q") != "go generics" || gotQuery.Get("t") != "week" || gotQuery.Get("sort") != "top" {
		t.Errorf("query = %v", gotQuery)
	}
	// The stale post is outside the window and must be dropped client-side:
	// the upstream time filter is coarse.
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(items), items)
	}
	if items[0].Score != 42 || items[0].Comments != 7 || items[0].Extra != "r/golang" {
		t.Errorf("item = %+v", items[0])
	}
}

func TestReddit_NoKey(t *testing.T) {
	if _, err := recent.NewReddit("").Fetch(context.Background(), "q", time.Now(), 5); err == nil {
		t.Fatal("expected error without api key")
	}
}

func TestTimeRangeFor(t *testing.T) {
	now := time.Now()
	cases := map[time.Duration]string{
		12 * time.Hour:       "day",
		3 * 24 * time.Hour:   "week",
		20 * 24 * time.Hour:  "month",
		200 * 24 * time.Hour: "year",
	}
	for age, want := range cases {
		if got := recent.TimeRangeFor(now.Add(-age)); got != want {
			t.Errorf("age %v: got %q want %q", age, got, want)
		}
	}
}
