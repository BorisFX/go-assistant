package openrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
)

func TestEmbeddingClient_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing or wrong Authorization header")
		}

		resp := openrouter.EmbeddingResponse{
			Data: []openrouter.EmbeddingData{
				{Embedding: make([]float32, 1536)},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := openrouter.NewEmbeddingClient("test-key", "text-embedding-3-small", server.URL+"/api/v1/embeddings")

	embedding, err := client.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 1536 {
		t.Errorf("expected 1536 dimensions, got %d", len(embedding))
	}
}
