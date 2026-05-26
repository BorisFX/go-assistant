package cryptoai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type StatusResponse struct {
	Balance        float64 `json:"balance"`
	OpenPositions  int     `json:"open_positions"`
	TotalPnL       float64 `json:"total_pnl"`
	TodayPnL       float64 `json:"today_pnl"`
	ActiveSymbols  int     `json:"active_symbols"`
	BotRunning     bool    `json:"bot_running"`
	LastSignalTime string  `json:"last_signal_time"`
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cryptoai error: status %d", resp.StatusCode)
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &status, nil
}
