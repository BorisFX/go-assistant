// Package tavily is a SearchProvider backed by the Tavily Search API
// (https://tavily.com) — a search engine purpose-built for LLM agents. Unlike a
// raw SearXNG/DDG scrape it returns clean, ranked content plus a published_date
// per result and supports a freshness filter, which is what keeps a "today"
// query from surfacing decade-old archive pages.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const defaultEndpoint = "https://api.tavily.com/search"

type Client struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

// New builds a Tavily client. endpoint may be empty to use the public API.
func New(apiKey, endpoint string) *Client {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	return &Client{
		apiKey:   apiKey,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type apiRequest struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	SearchDepth string `json:"search_depth"`
	Topic       string `json:"topic,omitempty"`
	TimeRange   string `json:"time_range,omitempty"`
}

type apiResponse struct {
	Results []apiResult `json:"results"`
}

type apiResult struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	PublishedDate string  `json:"published_date"`
	Score         float64 `json:"score"`
}

func (c *Client) Search(ctx context.Context, query string, opts output.SearchOptions) ([]output.SearchResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("tavily: api key not configured")
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}

	reqBody := apiRequest{
		Query:       query,
		MaxResults:  maxResults,
		SearchDepth: "basic",
		Topic:       normalizeTopic(opts.Topic),
		TimeRange:   normalizeTimeRange(opts.TimeRange),
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tavily: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: status %d", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}

	results := make([]output.SearchResult, 0, len(apiResp.Results))
	for _, r := range apiResp.Results {
		results = append(results, output.SearchResult{
			Title:         r.Title,
			URL:           r.URL,
			Content:       r.Content,
			PublishedDate: r.PublishedDate,
		})
	}
	return results, nil
}

// normalizeTopic keeps only the values Tavily accepts ("news" | "general").
// Anything else falls back to "general" — the whole-web corpus.
func normalizeTopic(topic string) string {
	if topic == "news" {
		return "news"
	}
	return "general"
}

// normalizeTimeRange passes through only Tavily's accepted ranges; unknown
// values are dropped so a bad filter never rejects the request.
func normalizeTimeRange(tr string) string {
	switch tr {
	case "day", "week", "month", "year":
		return tr
	default:
		return ""
	}
}
