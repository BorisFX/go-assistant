package recent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Polymarket reads live prediction-market odds through the public Gamma API.
// No key needed. Markets have no publish time, so `since` is not applied: the
// current price already is the freshest possible view.
type Polymarket struct {
	baseURL    string
	httpClient *http.Client
}

func NewPolymarket() *Polymarket {
	return &Polymarket{baseURL: "https://gamma-api.polymarket.com", httpClient: newHTTPClient()}
}

func (p *Polymarket) WithBaseURL(u string) *Polymarket {
	p.baseURL = u
	return p
}

func (p *Polymarket) Name() string { return "polymarket" }

type gammaEvent struct {
	Title   string        `json:"title"`
	Slug    string        `json:"slug"`
	EndDate string        `json:"endDate"`
	Volume  flexFloat     `json:"volume"`
	Closed  bool          `json:"closed"`
	Markets []gammaMarket `json:"markets"`
}

type gammaMarket struct {
	Question       string    `json:"question"`
	GroupItemTitle string    `json:"groupItemTitle"`
	OutcomePrices  string    `json:"outcomePrices"` // JSON array encoded as a string
	Volume         flexFloat `json:"volume"`
	Closed         bool      `json:"closed"`
}

// maxMarketsPerEvent caps the odds listed for a multi-outcome event.
const maxMarketsPerEvent = 4

func (p *Polymarket) Fetch(ctx context.Context, query string, _ time.Time, limit int) ([]Item, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit_per_type", strconv.Itoa(limit))

	var resp struct {
		Events []gammaEvent `json:"events"`
	}
	if err := getJSON(ctx, p.httpClient, p.baseURL+"/public-search?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("polymarket: %w", err)
	}

	open := make([]gammaEvent, 0, len(resp.Events))
	for _, ev := range resp.Events {
		if !ev.Closed {
			open = append(open, ev)
		}
	}
	// Search relevance is weak; volume is a better proxy for what matters.
	sort.Slice(open, func(i, j int) bool { return open[i].Volume > open[j].Volume })

	items := make([]Item, 0, len(open))
	for _, ev := range open {
		items = append(items, Item{
			Title: ev.Title,
			URL:   "https://polymarket.com/event/" + ev.Slug,
			Extra: describeEvent(ev),
		})
	}
	return items, nil
}

// describeEvent renders "25 bps: 63% · No change: 30% · vol $1.2M · ends 2026-10-29".
func describeEvent(ev gammaEvent) string {
	markets := make([]gammaMarket, 0, len(ev.Markets))
	for _, m := range ev.Markets {
		if !m.Closed {
			markets = append(markets, m)
		}
	}
	// Leading outcomes first: for "how many cuts?" the reader wants the 96%
	// answer, not the heavily traded 0% ones.
	sort.SliceStable(markets, func(i, j int) bool {
		pi, _ := yesProbability(markets[i].OutcomePrices)
		pj, _ := yesProbability(markets[j].OutcomePrices)
		return pi > pj
	})
	if len(markets) > maxMarketsPerEvent {
		markets = markets[:maxMarketsPerEvent]
	}

	parts := make([]string, 0, len(markets)+2)
	for _, m := range markets {
		label := m.GroupItemTitle
		if label == "" || len(markets) == 1 {
			label = "Yes"
		}
		if yes, ok := yesProbability(m.OutcomePrices); ok {
			parts = append(parts, fmt.Sprintf("%s: %.0f%%", label, yes*100))
		}
	}
	if ev.Volume > 0 {
		parts = append(parts, "vol "+formatUSD(float64(ev.Volume)))
	}
	if len(ev.EndDate) >= 10 {
		parts = append(parts, "ends "+ev.EndDate[:10])
	}
	return strings.Join(parts, " · ")
}

// yesProbability extracts the first outcome's price from Gamma's stringified
// array, e.g. "[\"0.63\",\"0.37\"]" → 0.63.
func yesProbability(encoded string) (float64, bool) {
	var prices []string
	if err := json.Unmarshal([]byte(encoded), &prices); err != nil || len(prices) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(prices[0], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func formatUSD(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("$%.0fK", v/1e3)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
