package output

import "context"

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	// PublishedDate is the article's publish date when the provider exposes it
	// (ISO-8601 or provider-native). Empty when unknown. Critical for freshness:
	// without it the model can't tell a 2010 archive page from a 2026 article.
	PublishedDate string `json:"published_date,omitempty"`
}

// SearchOptions carries the knobs a caller can turn per query. Zero values mean
// "provider default": MaxResults<=0 → provider picks, empty TimeRange → no age
// filter, empty Topic → general web.
type SearchOptions struct {
	MaxResults int
	// TimeRange restricts results by recency: "day", "week", "month", "year".
	// Empty = no restriction.
	TimeRange string
	// Topic selects the corpus: "news" biases toward dated news articles,
	// "general" (or empty) is the whole web.
	Topic string
}

type SearchProvider interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}
