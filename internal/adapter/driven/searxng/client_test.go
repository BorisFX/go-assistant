package searxng_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/searxng"
)

func TestClient_Search(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "Go developer Cambodia" {
			t.Errorf("unexpected query: %s", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("expected format=json, got %s", r.URL.Query().Get("format"))
		}

		resp := searxng.APIResponse{
			Results: []searxng.APIResult{
				{Title: "Job 1", URL: "https://example.com/1", Content: "Go developer needed"},
				{Title: "Job 2", URL: "https://example.com/2", Content: "Senior Go position"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := searxng.New(server.URL)
	results, err := client.Search(context.Background(), "Go developer Cambodia", 5)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "Job 1" {
		t.Errorf("expected title 'Job 1', got %s", results[0].Title)
	}
}
