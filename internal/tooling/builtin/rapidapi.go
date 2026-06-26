package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// RapidAPIClient is a thin GET-only JSON client for RapidAPI-hosted APIs
// (booking-com15). It injects the x-rapidapi-key / x-rapidapi-host headers.
type RapidAPIClient struct {
	httpClient *http.Client
	baseURL    string // overridable in tests
	host       string
	key        string
}

func NewRapidAPIClient(key, host string) *RapidAPIClient {
	return &RapidAPIClient{
		httpClient: &http.Client{Timeout: 45 * time.Second},
		baseURL:    "https://" + host,
		host:       host,
		key:        key,
	}
}

// getJSON performs GET {baseURL}{path}?{query} and decodes the body into out.
func (c *RapidAPIClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-rapidapi-key", c.key)
	req.Header.Set("x-rapidapi-host", c.host)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("rapidapi %s: status %d: %s", path, resp.StatusCode, snippet)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
