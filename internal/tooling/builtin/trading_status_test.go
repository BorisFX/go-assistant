package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type mockTradingClient struct{}

func (m *mockTradingClient) GetStatus(ctx context.Context) (*cryptoai.StatusResponse, error) {
	return &cryptoai.StatusResponse{
		Balance:       1006.50,
		OpenPositions: 0,
		TotalPnL:      6.50,
		BotRunning:    true,
	}, nil
}

func TestTradingStatusTool_Execute(t *testing.T) {
	tool := builtin.NewTradingStatus(&mockTradingClient{})

	if tool.Name() != "trading_status" {
		t.Errorf("expected name trading_status, got %s", tool.Name())
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var status cryptoai.StatusResponse
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatalf("invalid result: %v", err)
	}

	if status.Balance != 1006.50 {
		t.Errorf("expected balance 1006.50, got %f", status.Balance)
	}
}
