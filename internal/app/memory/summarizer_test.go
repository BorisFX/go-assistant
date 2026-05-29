package memory

import (
	"testing"
	"time"
)

func TestMissingDays(t *testing.T) {
	loc := time.UTC
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, loc) }

	withMessages := map[string]bool{
		"2026-05-21": true,
		"2026-05-22": true,
		"2026-05-23": true,
	}
	withSummary := map[string]bool{
		"2026-05-21": true,
	}
	today := d(2026, 5, 23) // 23rd is "today", must be excluded

	got := missingDays(withMessages, withSummary, today)

	if len(got) != 1 || got[0] != "2026-05-22" {
		t.Fatalf("expected [2026-05-22], got %v", got)
	}
}
