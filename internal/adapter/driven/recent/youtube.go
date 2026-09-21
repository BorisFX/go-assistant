package recent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// YouTube lists recent videos through the Data API v3 search endpoint. Needs
// an API key; one call costs 100 of the free 10,000 daily quota units, so no
// follow-up statistics request is made.
type YouTube struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewYouTube(apiKey string) *YouTube {
	return &YouTube{baseURL: "https://www.googleapis.com", apiKey: apiKey, httpClient: newHTTPClient()}
}

func (y *YouTube) WithBaseURL(u string) *YouTube {
	y.baseURL = u
	return y
}

func (y *YouTube) Name() string { return "youtube" }

type youtubeResponse struct {
	Items []struct {
		ID struct {
			VideoID string `json:"videoId"`
		} `json:"id"`
		Snippet struct {
			Title        string `json:"title"`
			Description  string `json:"description"`
			PublishedAt  string `json:"publishedAt"`
			ChannelTitle string `json:"channelTitle"`
		} `json:"snippet"`
	} `json:"items"`
}

func (y *YouTube) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error) {
	if y.apiKey == "" {
		return nil, fmt.Errorf("youtube: api key not configured")
	}
	if limit > 50 {
		limit = 50 // API maximum
	}

	q := url.Values{}
	q.Set("part", "snippet")
	q.Set("type", "video")
	q.Set("order", "relevance")
	q.Set("q", query)
	q.Set("publishedAfter", since.UTC().Format(time.RFC3339))
	q.Set("maxResults", strconv.Itoa(limit))
	q.Set("key", y.apiKey)

	var resp youtubeResponse
	if err := getJSON(ctx, y.httpClient, y.baseURL+"/youtube/v3/search?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("youtube: %w", err)
	}

	items := make([]Item, 0, len(resp.Items))
	for _, it := range resp.Items {
		if it.ID.VideoID == "" {
			continue
		}
		published, _ := time.Parse(time.RFC3339, it.Snippet.PublishedAt)
		items = append(items, Item{
			Title:     stripTags(it.Snippet.Title),
			URL:       "https://www.youtube.com/watch?v=" + it.ID.VideoID,
			Published: published,
			Snippet:   it.Snippet.Description,
			Extra:     it.Snippet.ChannelTitle,
		})
	}
	return items, nil
}
