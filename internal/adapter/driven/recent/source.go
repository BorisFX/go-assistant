// Package recent gathers what people are saying about a topic right now: fresh
// threads from discussion platforms and live odds from prediction markets. It
// complements web search, which answers "what is X" — this answers "what is
// being said about X this week, and what does the market believe".
package recent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Item is one dated hit from a source. Score/Comments are zero when the source
// has no such notion (YouTube search, Polymarket), Published is zero when the
// source gives no date (a market has a state, not a publish time).
type Item struct {
	Title     string
	URL       string
	Published time.Time
	Score     int
	Comments  int
	Snippet   string
	// Extra is a one-line, source-specific annotation: the subreddit, the
	// channel, the market odds. Kept as text so the tool stays source-agnostic.
	Extra string
}

// Source is one platform. Fetch returns up to limit items about query that are
// newer than since; a source without dates returns its current state.
type Source interface {
	Name() string
	Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error)
}

// TimeRangeFor maps a lower bound to the coarse day/week/month/year buckets
// that search APIs (Reddit, Tavily) accept. Callers still filter by exact time.
// A bound computed as "now minus 7 days" is a hair older than a week by the
// time it gets here, so each bucket gets an hour of slack.
func TimeRangeFor(since time.Time) string {
	const slack = time.Hour
	age := time.Since(since)
	switch {
	case age <= 24*time.Hour+slack:
		return "day"
	case age <= 7*24*time.Hour+slack:
		return "week"
	case age <= 31*24*time.Hour+slack:
		return "month"
	default:
		return "year"
	}
}

// WithFallback returns a Source that answers from primary and, only when
// primary fails, from secondary. An empty primary result stands: the fallback
// exists for outages, not for padding.
func WithFallback(primary, secondary Source) Source {
	return &fallback{primary: primary, secondary: secondary}
}

type fallback struct {
	primary, secondary Source
}

func (f *fallback) Name() string { return f.primary.Name() }

func (f *fallback) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]Item, error) {
	items, err := f.primary.Fetch(ctx, query, since, limit)
	if err == nil {
		return items, nil
	}
	slog.Warn("recent source failed, using fallback", "source", f.primary.Name(), "error", err)
	return f.secondary.Fetch(ctx, query, since, limit)
}

// flexInt decodes a JSON number or a numeric string. redditapis.com returns
// counts as strings, Gamma returns volumes as either.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("flexInt %q: %w", s, err)
	}
	*n = flexInt(f)
	return nil
}

type flexFloat float64

func (n *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("flexFloat %q: %w", s, err)
	}
	*n = flexFloat(f)
	return nil
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// stripTags turns a snippet of HTML into plain text good enough for a preview.
func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#x27;", "'", "&#39;", "'").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// getJSON performs a GET with optional headers and decodes a 200 body into out.
// Non-200 responses become errors carrying a short body excerpt for the log.
func getJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "go-assistant/1.0 (personal research tool)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, snippet)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}
