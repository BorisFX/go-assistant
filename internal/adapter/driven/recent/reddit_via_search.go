package recent

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// RedditViaSearch finds Reddit threads through a general web-search provider
// restricted to reddit.com. It is the outage fallback for the direct client:
// no upvote counts, dates only when the provider has them, but always available.
type RedditViaSearch struct {
	provider output.SearchProvider
}

func NewRedditViaSearch(provider output.SearchProvider) *RedditViaSearch {
	return &RedditViaSearch{provider: provider}
}

func (r *RedditViaSearch) Name() string { return "reddit" }

var subredditRe = regexp.MustCompile(`reddit\.com/(r/[^/]+)`)

func (r *RedditViaSearch) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error) {
	results, err := r.provider.Search(ctx, query, output.SearchOptions{
		MaxResults:     limit,
		TimeRange:      TimeRangeFor(since),
		IncludeDomains: []string{"reddit.com"},
	})
	if err != nil {
		return nil, fmt.Errorf("reddit via search: %w", err)
	}

	items := make([]Item, 0, len(results))
	for _, res := range results {
		item := Item{
			Title:     res.Title,
			URL:       res.URL,
			Published: parseLooseDate(res.PublishedDate),
			Snippet:   res.Content,
		}
		if m := subredditRe.FindStringSubmatch(res.URL); m != nil {
			item.Extra = m[1]
		}
		items = append(items, item)
	}
	return items, nil
}

// parseLooseDate accepts the date formats search providers actually emit.
// Unknown or empty input yields the zero time rather than a wrong date.
func parseLooseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
		time.RFC1123,
		time.RFC1123Z,
		"Mon, 02 Jan 2006 15:04:05 MST",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
