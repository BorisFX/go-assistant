package recent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// HackerNews searches stories through the public Algolia index. No key needed.
type HackerNews struct {
	baseURL    string
	httpClient *http.Client
}

func NewHackerNews() *HackerNews {
	return &HackerNews{baseURL: "https://hn.algolia.com", httpClient: newHTTPClient()}
}

// WithBaseURL points the client at a test server.
func (h *HackerNews) WithBaseURL(u string) *HackerNews {
	h.baseURL = u
	return h
}

func (h *HackerNews) Name() string { return "hackernews" }

type hnResponse struct {
	Hits []struct {
		ObjectID    string `json:"objectID"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		Points      int    `json:"points"`
		NumComments int    `json:"num_comments"`
		CreatedAtI  int64  `json:"created_at_i"`
		StoryText   string `json:"story_text"`
	} `json:"hits"`
}

func (h *HackerNews) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("tags", "story")
	q.Set("numericFilters", "created_at_i>"+strconv.FormatInt(since.Unix(), 10))
	q.Set("hitsPerPage", strconv.Itoa(limit))

	var resp hnResponse
	if err := getJSON(ctx, h.httpClient, h.baseURL+"/api/v1/search?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}

	items := make([]Item, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		link := hit.URL
		if link == "" {
			// Ask HN / Show HN text posts have no external link.
			link = "https://news.ycombinator.com/item?id=" + hit.ObjectID
		}
		items = append(items, Item{
			Title:     hit.Title,
			URL:       link,
			Published: time.Unix(hit.CreatedAtI, 0).UTC(),
			Score:     hit.Points,
			Comments:  hit.NumComments,
			Snippet:   stripTags(hit.StoryText),
			Extra:     "https://news.ycombinator.com/item?id=" + hit.ObjectID,
		})
	}
	return items, nil
}
