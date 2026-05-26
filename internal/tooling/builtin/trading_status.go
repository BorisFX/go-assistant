package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
)

type TradingClient interface {
	GetStatus(ctx context.Context) (*cryptoai.StatusResponse, error)
}

type TradingStatus struct {
	client TradingClient
}

func NewTradingStatus(client TradingClient) *TradingStatus {
	return &TradingStatus{client: client}
}

func (t *TradingStatus) Name() string        { return "trading_status" }
func (t *TradingStatus) Description() string { return "Get current trading bot status: balance, positions, P&L" }
func (t *TradingStatus) Category() string    { return "trading" }

func (t *TradingStatus) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

func (t *TradingStatus) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	status, err := t.client.GetStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("get trading status: %w", err)
	}

	return json.Marshal(status)
}
