package openrouter_test

import (
	"context"
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
