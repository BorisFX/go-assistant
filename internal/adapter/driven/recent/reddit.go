package recent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Reddit searches public posts sitewide through redditapis.com, a third-party
// read-only proxy with the same q/sort/t parameters as Reddit's own /search.
// Reddit's first-party Data API requires a manual approval we do not have yet;
// when it comes, only the base URL, auth header and response shape change.
type Reddit struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewReddit(apiKey string) *Reddit {
	return &Reddit{baseURL: "https://api.redditapis.com", apiKey: apiKey, httpClient: newHTTPClient()}
}

func (r *Reddit) WithBaseURL(u string) *Reddit {
	r.baseURL = u
	return r
}

func (r *Reddit) Name() string { return "reddit" }

type redditPost struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Permalink  string  `json:"permalink"`
	Text       string  `json:"text"`
	Subreddit  string  `json:"subreddit"`
	Upvotes    flexInt `json:"upvotes"`
	Comments   flexInt `json:"comments"`
	CreatedUTC flexInt `json:"created_utc"`
}

func (r *Reddit) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error) {
	if r.apiKey == "" {
		return nil, fmt.Errorf("reddit: api key not configured")
	}

	q := url.Values{}
	q.Set("q", query)
	// "top" within the window ranks by what the community actually engaged
	// with; "new" would surface unanswered posts from a minute ago.
	q.Set("sort", "top")
	q.Set("t", TimeRangeFor(since))
	q.Set("limit", strconv.Itoa(limit))

	var resp struct {
		Posts []redditPost `json:"posts"`
	}
	headers := map[string]string{"Authorization": "Bearer " + r.apiKey}
	if err := getJSON(ctx, r.httpClient, r.baseURL+"/api/reddit/search?"+q.Encode(), headers, &resp); err != nil {
		return nil, fmt.Errorf("reddit: %w", err)
	}

	items := make([]Item, 0, len(resp.Posts))
	for _, p := range resp.Posts {
		created := time.Unix(int64(p.CreatedUTC), 0).UTC()
		// The upstream t= bucket is coarser than the caller's window.
		if created.Before(since) {
			continue
		}
		link := p.URL
		if link == "" {
			link = "https://reddit.com" + p.Permalink
		}
		items = append(items, Item{
			Title:     p.Title,
			URL:       link,
			Published: created,
			Score:     int(p.Upvotes),
			Comments:  int(p.Comments),
			Snippet:   stripTags(p.Text),
			Extra:     "r/" + p.Subreddit,
		})
	}
	return items, nil
}
