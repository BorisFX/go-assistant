package openrouter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// The a6api gateway load-balances over several origins and one of them can be
// broken. Retrying over the same kept-alive connection lands on the same broken
// origin every time, so a retry is only worth anything on a fresh connection.
func TestRetryAfterServerErrorUsesAFreshConnection(t *testing.T) {
	var (
		mu    sync.Mutex
		conns []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns = append(conns, r.RemoteAddr)
		attempt := len(conns)
		mu.Unlock()

		if attempt == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "<html>502 Origin Not Reachable</html>")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"привет"}}]}`)
	}))
	defer srv.Close()

	client := openrouter.New("key", "gemini-3.8-flash", "", srv.URL)

	resp, err := client.Chat(context.Background(), output.LLMRequest{
		Messages: []output.LLMMessage{{Role: "user", Content: "привет"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "привет" {
		t.Errorf("content = %q, want the retried response", resp.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(conns) < 2 {
		t.Fatalf("server saw %d requests, want the failed one retried", len(conns))
	}
	if conns[0] == conns[1] {
		t.Errorf("retry reused connection %s — a broken origin would answer it again", conns[0])
	}
}

// The gateway sometimes answers 200 with a non-JSON body (an SSE "data: ..."
// stream or a plain-text error). That is a broken origin, not a real answer,
// so it must be retried on a fresh connection instead of failing the chat.
func TestRetryAfterNonJSONSuccessBody(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			fmt.Fprint(w, "data: {\"choices\":[]}\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ок"}}]}`)
	}))
	defer srv.Close()

	client := openrouter.New("key", "gemini-3.8-flash", "", srv.URL)
	resp, err := client.Chat(context.Background(), output.LLMRequest{
		Messages: []output.LLMMessage{{Role: "user", Content: "привет"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ок" {
		t.Errorf("content = %q, want the retried response", resp.Content)
	}
}

// When the primary chat model keeps failing, the configured fallback model
// answers instead of the user getting an error.
func TestChatFallsBackToFallbackModel(t *testing.T) {
	var (
		mu     sync.Mutex
		models []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		models = append(models, body.Model)
		mu.Unlock()
		if body.Model == "primary" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"invalid_request"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"запасной"}}]}`)
	}))
	defer srv.Close()

	client := openrouter.New("key", "primary", "backup", srv.URL)
	resp, err := client.Chat(context.Background(), output.LLMRequest{
		Messages: []output.LLMMessage{{Role: "user", Content: "привет"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "запасной" {
		t.Errorf("content = %q, want the fallback answer", resp.Content)
	}

	// A caller that pins a model gets its own error, not a silent model swap.
	if _, err := client.Chat(context.Background(), output.LLMRequest{
		Model:    "primary",
		Messages: []output.LLMMessage{{Role: "user", Content: "привет"}},
	}); err == nil {
		t.Error("pinned model fell back, want its error")
	}
}
