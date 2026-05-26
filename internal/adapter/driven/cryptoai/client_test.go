package cryptoai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
)

func TestClient_GetStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Error("missing API key")
		}

		resp := cryptoai.StatusResponse{
			Balance:        1006.50,
			OpenPositions:  0,
			TotalPnL:       6.50,
			TodayPnL:       0.00,
			ActiveSymbols:  7,
			BotRunning:     true,
			LastSignalTime: "2026-05-21T14:32:00Z",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := cryptoai.New(server.URL, "test-key")
	status, err := client.GetStatus(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.Balance != 1006.50 {
		t.Errorf("expected balance 1006.50, got %f", status.Balance)
	}

	if !status.BotRunning {
		t.Error("expected bot to be running")
	}
}
