package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/recent"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeSource struct {
	name     string
	items    []recent.Item
	err      error
	delay    time.Duration
	gotSince time.Time
	gotLimit int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(ctx context.Context, query string, since time.Time, limit int) ([]recent.Item, error) {
	f.gotSince = since
	f.gotLimit = limit
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.items, f.err
}

type researchOut struct {
	Query   string `json:"query"`
	Since   string `json:"since"`
	Sources map[string]struct {
		Items []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Date    string `json:"date"`
			Score   int    `json:"score"`
			Snippet string `json:"snippet"`
			Extra   string `json:"extra"`
		} `json:"items"`
		Error string `json:"error"`
	} `json:"sources"`
}

func run(t *testing.T, tool *builtin.ResearchRecent, params string) researchOut {
	t.Helper()
	raw, err := tool.Execute(context.Background(), json.RawMessage(params))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out researchOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid result json: %v\n%s", err, raw)
	}
	return out
}

func TestResearchRecent_Metadata(t *testing.T) {
	tool := builtin.NewResearchRecent()
	if tool.Name() != "research_recent" || tool.Category() != "search" {
		t.Errorf("name=%q category=%q", tool.Name(), tool.Category())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestResearchRecent_FanOutTolerantOfFailures(t *testing.T) {
	now := time.Now()
	hn := &fakeSource{name: "hackernews", items: []recent.Item{
		{Title: "older", URL: "u1", Published: now.Add(-3 * 24 * time.Hour), Score: 10},
		{Title: "newest", URL: "u2", Published: now.Add(-1 * time.Hour), Score: 5, Snippet: strings.Repeat("x", 500)},
	}}
	reddit := &fakeSource{name: "reddit", err: errors.New("upstream 502")}
	tool := builtin.NewResearchRecent(hn, reddit)

	out := run(t, tool, `{"query":"claude code","days":3}`)

	if out.Query != "claude code" {
		t.Errorf("query = %q", out.Query)
	}
	wantSince := now.Add(-3 * 24 * time.Hour)
	if d := hn.gotSince.Sub(wantSince); d < -time.Minute || d > time.Minute {
		t.Errorf("since = %v, want ~%v", hn.gotSince, wantSince)
	}
	hnOut := out.Sources["hackernews"]
	if len(hnOut.Items) != 2 || hnOut.Items[0].Title != "newest" {
		t.Errorf("hn items should be newest-first: %+v", hnOut.Items)
	}
	if len(hnOut.Items[0].Snippet) > 310 {
		t.Errorf("snippet not truncated: len %d", len(hnOut.Items[0].Snippet))
	}
	if !strings.Contains(out.Sources["reddit"].Error, "502") {
		t.Errorf("reddit error not reported: %+v", out.Sources["reddit"])
	}
}

func TestResearchRecent_DefaultsAndCaps(t *testing.T) {
	items := make([]recent.Item, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, recent.Item{Title: "t", URL: "u", Published: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	src := &fakeSource{name: "hackernews", items: items}
	tool := builtin.NewResearchRecent(src)

	out := run(t, tool, `{"query":"q"}`)

	if got := len(out.Sources["hackernews"].Items); got != 8 {
		t.Errorf("expected cap of 8 items, got %d", got)
	}
	// Default window is a week.
	if d := time.Since(src.gotSince); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Errorf("default since = %v ago", d)
	}
	if src.gotLimit <= 0 {
		t.Errorf("limit must be positive, got %d", src.gotLimit)
	}
}

func TestResearchRecent_SourceFilter(t *testing.T) {
	hn := &fakeSource{name: "hackernews", items: []recent.Item{{Title: "a"}}}
	yt := &fakeSource{name: "youtube", items: []recent.Item{{Title: "b"}}}
	tool := builtin.NewResearchRecent(hn, yt)

	out := run(t, tool, `{"query":"q","sources":["youtube"]}`)

	if _, ok := out.Sources["hackernews"]; ok {
		t.Error("hackernews should have been skipped")
	}
	if len(out.Sources["youtube"].Items) != 1 {
		t.Errorf("youtube = %+v", out.Sources["youtube"])
	}
}

func TestResearchRecent_SlowSourceTimesOut(t *testing.T) {
	slow := &fakeSource{name: "reddit", delay: time.Second, items: []recent.Item{{Title: "late"}}}
	fast := &fakeSource{name: "hackernews", items: []recent.Item{{Title: "fast"}}}
	tool := builtin.NewResearchRecent(fast, slow).WithTimeout(50 * time.Millisecond)

	start := time.Now()
	out := run(t, tool, `{"query":"q"}`)
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("slow source should not block the answer, took %v", time.Since(start))
	}
	if out.Sources["reddit"].Error == "" {
		t.Errorf("timed-out source must report an error: %+v", out.Sources["reddit"])
	}
	if len(out.Sources["hackernews"].Items) != 1 {
		t.Errorf("fast source lost: %+v", out.Sources["hackernews"])
	}
}

func TestResearchRecent_AllFailedIsError(t *testing.T) {
	tool := builtin.NewResearchRecent(&fakeSource{name: "reddit", err: errors.New("down")})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"q"}`)); err == nil {
		t.Fatal("expected error when every source failed")
	}
}

func TestResearchRecent_MissingQuery(t *testing.T) {
	tool := builtin.NewResearchRecent(&fakeSource{name: "hackernews"})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing query")
	}
}
