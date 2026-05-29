package cron

import (
	"testing"
	"time"
)

func TestComputeNextInterval(t *testing.T) {
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	loc := time.UTC

	cases := []struct {
		schedule string
		wantSec  int
	}{
		{"every 30m", 1800},
		{"every 2h", 7200},
		{"every 1h", 3600},
		{"hourly", 3600},
		{"daily", 86400},
		{"45m", 2700},
	}
	for _, c := range cases {
		next, sec, err := ComputeNext(c.schedule, from, loc)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.schedule, err)
		}
		if sec != c.wantSec {
			t.Errorf("%q: interval = %d, want %d", c.schedule, sec, c.wantSec)
		}
		want := from.Add(time.Duration(c.wantSec) * time.Second)
		if !next.Equal(want) {
			t.Errorf("%q: next = %v, want %v", c.schedule, next, want)
		}
	}
}

func TestComputeNextClockTimeLaterToday(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Phnom_Penh") // UTC+7
	// 08:00 local -> next "daily at 09:00" is 09:00 same day.
	from := time.Date(2026, 5, 29, 8, 0, 0, 0, loc)

	for _, schedule := range []string{"daily at 09:00", "every day at 9:00", "at 09:00"} {
		next, sec, err := ComputeNext(schedule, from, loc)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", schedule, err)
		}
		if sec != 86400 {
			t.Errorf("%q: interval = %d, want 86400", schedule, sec)
		}
		want := time.Date(2026, 5, 29, 9, 0, 0, 0, loc)
		if !next.Equal(want) {
			t.Errorf("%q: next = %v, want %v", schedule, next, want)
		}
	}
}

func TestComputeNextClockTimeRollsToTomorrow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Phnom_Penh")
	// 10:00 local, target 09:00 already passed -> tomorrow 09:00.
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, loc)

	next, _, err := ComputeNext("daily at 09:00", from, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 5, 30, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestComputeNextClockTimeRespectsTimezone(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow") // UTC+3
	// from is 05:00 UTC = 08:00 MSK; target 09:00 MSK is later today.
	from := time.Date(2026, 5, 29, 5, 0, 0, 0, time.UTC)

	next, _, err := ComputeNext("daily at 09:00", from, msk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 5, 29, 9, 0, 0, 0, msk)
	if !next.Equal(want) {
		t.Errorf("next = %v (%v), want %v", next, next.UTC(), want)
	}
}

func TestComputeNextErrors(t *testing.T) {
	from := time.Now()
	for _, schedule := range []string{"at 25:00", "at 09:99", "gibberish", "daily at noon"} {
		if _, _, err := ComputeNext(schedule, from, time.UTC); err == nil {
			t.Errorf("%q: expected error, got nil", schedule)
		}
	}
}

func TestComputeNextNilLocationUsesUTC(t *testing.T) {
	from := time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC)
	next, _, err := ComputeNext("at 09:00", from, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}
