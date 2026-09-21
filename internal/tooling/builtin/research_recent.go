package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/recent"
)

const (
	researchDefaultDays   = 7
	researchMaxDays       = 30
	researchItemsPerSrc   = 8
	researchFetchLimit    = 15 // ask for more than we show so date filtering leaves enough
	researchSnippetRunes  = 300
	researchSourceTimeout = 12 * time.Second
)

// ResearchRecent fans one topic out to discussion platforms and prediction
// markets in parallel and returns their fresh hits side by side. It does not
// summarise: the chat model reads the JSON and writes the synthesis, as it does
// for search_web. One failed source is reported inline, not fatal.
type ResearchRecent struct {
	sources []recent.Source
	timeout time.Duration
}

func NewResearchRecent(sources ...recent.Source) *ResearchRecent {
	return &ResearchRecent{sources: sources, timeout: researchSourceTimeout}
}

// WithTimeout bounds each source's fetch (tests use a short one).
func (r *ResearchRecent) WithTimeout(d time.Duration) *ResearchRecent {
	r.timeout = d
	return r
}

func (r *ResearchRecent) Name() string     { return "research_recent" }
func (r *ResearchRecent) Category() string { return "search" }

func (r *ResearchRecent) Description() string {
	names := make([]string, 0, len(r.sources))
	for _, s := range r.sources {
		names = append(names, s.Name())
	}
	return "Find out what people are saying about a topic RIGHT NOW across discussion platforms " +
		"(" + strings.Join(names, ", ") + "): fresh threads with upvotes and comment counts, " +
		"recent videos, and live prediction-market odds. Use this for 'what do people think about X', " +
		"'what's the buzz / trend / sentiment', 'what's new with X this week', or when the user names " +
		"Reddit, Hacker News, YouTube or Polymarket. Use search_web instead for facts, definitions, " +
		"prices, official pages or news articles. Pass a short English keyword query (2-5 words) — " +
		"these platforms are English-first, so translate a Russian topic. Results are per source, " +
		"newest first; a source with an \"error\" field was unavailable, just say so briefly."
}

func (r *ResearchRecent) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Short keyword topic in English, 2-5 words. Good: \"Claude Code\", \"Fed rate cut\", \"Phnom Penh expat\". Bad: a full question."
			},
			"days": {
				"type": "integer",
				"description": "How far back to look, 1-30 days. Default 7. Use 1-2 for 'today', 30 for 'this month'.",
				"default": 7
			},
			"sources": {
				"type": "array",
				"items": {"type": "string", "enum": ["hackernews", "reddit", "youtube", "polymarket"]},
				"description": "Restrict to specific platforms. Omit to query all configured ones."
			}
		},
		"required": ["query"]
	}`)
}

type researchParams struct {
	Query   string   `json:"query"`
	Days    int      `json:"days"`
	Sources []string `json:"sources"`
}

type researchItem struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Date     string `json:"date,omitempty"`
	Score    int    `json:"score,omitempty"`
	Comments int    `json:"comments,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	Extra    string `json:"extra,omitempty"`
}

type researchSourceResult struct {
	Items []researchItem `json:"items,omitempty"`
	Error string         `json:"error,omitempty"`
}

type researchResult struct {
	Query   string                          `json:"query"`
	Since   string                          `json:"since"`
	Sources map[string]researchSourceResult `json:"sources"`
}

func (r *ResearchRecent) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p researchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	p.Query = strings.TrimSpace(p.Query)
	if p.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if p.Days <= 0 {
		p.Days = researchDefaultDays
	}
	if p.Days > researchMaxDays {
		p.Days = researchMaxDays
	}
	since := time.Now().Add(-time.Duration(p.Days) * 24 * time.Hour)

	selected := r.selectSources(p.Sources)
	if len(selected) == 0 {
		return nil, fmt.Errorf("no matching sources; available: %s", strings.Join(r.sourceNames(), ", "))
	}

	type outcome struct {
		name  string
		items []recent.Item
		err   error
	}
	outcomes := make([]outcome, len(selected))
	var wg sync.WaitGroup
	for i, src := range selected {
		wg.Add(1)
		go func(i int, src recent.Source) {
			defer wg.Done()
			fetchCtx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			items, err := src.Fetch(fetchCtx, p.Query, since, researchFetchLimit)
			outcomes[i] = outcome{name: src.Name(), items: items, err: err}
		}(i, src)
	}
	wg.Wait()

	result := researchResult{
		Query:   p.Query,
		Since:   since.UTC().Format("2006-01-02"),
		Sources: make(map[string]researchSourceResult, len(outcomes)),
	}
	failed := 0
	for _, o := range outcomes {
		if o.err != nil {
			failed++
			result.Sources[o.name] = researchSourceResult{Error: o.err.Error()}
			continue
		}
		result.Sources[o.name] = researchSourceResult{Items: shapeItems(o.items)}
	}
	if failed == len(outcomes) {
		return nil, fmt.Errorf("every source failed: %s", describeFailures(result.Sources))
	}
	return json.Marshal(result)
}

// selectSources returns the configured sources matching the requested names,
// or all of them when no filter is given. Unknown names are ignored.
func (r *ResearchRecent) selectSources(names []string) []recent.Source {
	if len(names) == 0 {
		return r.sources
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	out := make([]recent.Source, 0, len(r.sources))
	for _, s := range r.sources {
		if want[s.Name()] {
			out = append(out, s)
		}
	}
	return out
}

func (r *ResearchRecent) sourceNames() []string {
	names := make([]string, 0, len(r.sources))
	for _, s := range r.sources {
		names = append(names, s.Name())
	}
	return names
}

// shapeItems orders newest-first (undated items keep their source order at
// the end), caps the count and trims snippets to keep the tool result small.
func shapeItems(items []recent.Item) []researchItem {
	sorted := make([]recent.Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Published, sorted[j].Published
		if a.IsZero() || b.IsZero() {
			return !a.IsZero() && b.IsZero()
		}
		return a.After(b)
	})
	if len(sorted) > researchItemsPerSrc {
		sorted = sorted[:researchItemsPerSrc]
	}

	out := make([]researchItem, 0, len(sorted))
	for _, it := range sorted {
		ri := researchItem{
			Title:    it.Title,
			URL:      it.URL,
			Score:    it.Score,
			Comments: it.Comments,
			Snippet:  truncateRunes(it.Snippet, researchSnippetRunes),
			Extra:    it.Extra,
		}
		if !it.Published.IsZero() {
			ri.Date = it.Published.UTC().Format("2006-01-02")
		}
		out = append(out, ri)
	}
	return out
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func describeFailures(sources map[string]researchSourceResult) string {
	parts := make([]string, 0, len(sources))
	for name, res := range sources {
		parts = append(parts, name+": "+res.Error)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}
