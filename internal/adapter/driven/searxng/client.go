package searxng

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type APIResponse struct {
	Results []APIResult `json:"results"`
}

type APIResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Search(ctx context.Context, query string, maxResults int) ([]output.SearchResult, error) {
	// Try SearXNG first
	results, err := c.searchSearXNG(ctx, query, maxResults)
	if err == nil && len(results) > 0 {
		return results, nil
	}

	// Fallback to DuckDuckGo
	return c.searchDuckDuckGo(ctx, query, maxResults)
}

func (c *Client) searchSearXNG(ctx context.Context, query string, maxResults int) ([]output.SearchResult, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("searxng not configured")
	}

	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("pageno", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng status: %d", resp.StatusCode)
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return convertResults(apiResp.Results, maxResults), nil
}

// DuckDuckGo JSON API (no key required)
func (c *Client) searchDuckDuckGo(ctx context.Context, query string, maxResults int) ([]output.SearchResult, error) {
	ddgURL := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"

	req, err := http.NewRequestWithContext(ctx, "GET", ddgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create ddg request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ddg request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ddg response: %w", err)
	}

	var ddgResp struct {
		Abstract       string `json:"Abstract"`
		AbstractURL    string `json:"AbstractURL"`
		AbstractSource string `json:"AbstractSource"`
		RelatedTopics  []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		Results []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"Results"`
	}

	if err := json.Unmarshal(body, &ddgResp); err != nil {
		return nil, fmt.Errorf("decode ddg: %w", err)
	}

	var results []output.SearchResult

	if ddgResp.Abstract != "" {
		results = append(results, output.SearchResult{
			Title:   ddgResp.AbstractSource,
			URL:     ddgResp.AbstractURL,
			Content: ddgResp.Abstract,
		})
	}

	for _, r := range ddgResp.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, output.SearchResult{
			Title:   extractTitle(r.Text),
			URL:     r.FirstURL,
			Content: r.Text,
		})
	}

	for _, r := range ddgResp.RelatedTopics {
		if len(results) >= maxResults {
			break
		}
		if r.FirstURL != "" {
			results = append(results, output.SearchResult{
				Title:   extractTitle(r.Text),
				URL:     r.FirstURL,
				Content: r.Text,
			})
		}
	}

	if len(results) == 0 {
		// DDG instant answers didn't return results, fall back to HTML lite scrape
		return c.searchDDGLite(ctx, query, maxResults)
	}

	return results, nil
}

// DDG Lite — HTML scraping fallback for when instant answers are empty
func (c *Client) searchDDGLite(ctx context.Context, query string, maxResults int) ([]output.SearchResult, error) {
	liteURL := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, "GET", liteURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create lite request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lite request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read lite response: %w", err)
	}

	html := string(body)
	var results []output.SearchResult

	// Extract results from DDG lite HTML
	linkRe := regexp.MustCompile(`<a[^>]+rel="nofollow"[^>]+href="([^"]+)"[^>]*>([^<]+)</a>`)
	snippetRe := regexp.MustCompile(`<td class="result-snippet">([^<]+)</td>`)

	links := linkRe.FindAllStringSubmatch(html, maxResults*2)
	snippets := snippetRe.FindAllStringSubmatch(html, maxResults*2)

	for i, link := range links {
		if len(results) >= maxResults {
			break
		}
		if len(link) < 3 {
			continue
		}
		u := link[1]
		if strings.HasPrefix(u, "//duckduckgo.com") {
			continue
		}
		snippet := ""
		if i < len(snippets) && len(snippets[i]) > 1 {
			snippet = strings.TrimSpace(snippets[i][1])
		}
		results = append(results, output.SearchResult{
			Title:   strings.TrimSpace(link[2]),
			URL:     u,
			Content: snippet,
		})
	}

	return results, nil
}

func convertResults(apiResults []APIResult, maxResults int) []output.SearchResult {
	results := make([]output.SearchResult, 0, maxResults)
	for i, r := range apiResults {
		if i >= maxResults {
			break
		}
		results = append(results, output.SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
		})
	}
	return results
}

func extractTitle(text string) string {
	if idx := strings.Index(text, " - "); idx > 0 && idx < 100 {
		return text[:idx]
	}
	if len(text) > 80 {
		return text[:80] + "..."
	}
	return text
}
