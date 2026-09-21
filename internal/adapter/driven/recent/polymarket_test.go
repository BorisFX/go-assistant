package recent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/recent"
)

// Gamma nests outcomePrices as a JSON string inside JSON and mixes closed
// markets into search results; both have to be handled.
const polymarketPayload = `{"events":[
 {"title":"Old closed event","slug":"old","endDate":"2026-02-23T17:00:00Z","volume":9999999,"active":true,"closed":true,
  "markets":[{"question":"Closed?","outcomePrices":"[\"1\",\"0\"]","volume":100,"closed":true}]},
 {"title":"Fed rate cut in October?","slug":"fed-october","endDate":"2026-10-29T00:00:00Z","volume":"1234567.5","active":true,"closed":false,
  "markets":[
   {"question":"25 bps cut","groupItemTitle":"25 bps","outcomePrices":"[\"0.63\",\"0.37\"]","volume":"900000","closed":false},
   {"question":"No change","groupItemTitle":"No change","outcomePrices":"[\"0.30\",\"0.70\"]","volume":300000,"closed":false},
   {"question":"Resolved sub-market","groupItemTitle":"50 bps","outcomePrices":"[\"0\",\"1\"]","volume":10,"closed":true}
  ]},
 {"title":"Small event","slug":"small","endDate":"2026-12-31T00:00:00Z","volume":10,"active":true,"closed":false,
  "markets":[{"question":"Will it happen?","outcomePrices":"[\"0.05\",\"0.95\"]","volume":10,"closed":false}]}
]}`

func TestPolymarket_Fetch(t *testing.T) {
	var gotQ string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		w.Write([]byte(polymarketPayload))
	}))
	defer server.Close()

	src := recent.NewPolymarket().WithBaseURL(server.URL)
	items, err := src.Fetch(context.Background(), "fed rate", time.Now().Add(-7*24*time.Hour), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Name() != "polymarket" {
		t.Errorf("name = %q", src.Name())
	}
	if gotQ != "fed rate" {
		t.Errorf("q = %q", gotQ)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 open events, got %d: %+v", len(items), items)
	}
	// Highest volume first.
	if items[0].Title != "Fed rate cut in October?" {
		t.Errorf("item 0 title = %q", items[0].Title)
	}
	if items[0].URL != "https://polymarket.com/event/fed-october" {
		t.Errorf("item 0 url = %q", items[0].URL)
	}
	// Open markets by Yes-probability, most likely first; closed sub-market dropped.
	if !strings.HasPrefix(items[0].Extra, "25 bps: 63% · No change: 30%") {
		t.Errorf("item 0 extra = %q", items[0].Extra)
	}
	if strings.Contains(items[0].Extra, "50 bps") {
		t.Errorf("closed sub-market leaked into extra: %q", items[0].Extra)
	}
	if !strings.Contains(items[0].Extra, "$1.2M") {
		t.Errorf("volume missing from extra: %q", items[0].Extra)
	}
	if !strings.Contains(items[0].Extra, "2026-10-29") {
		t.Errorf("end date missing from extra: %q", items[0].Extra)
	}
}
