package cron_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/cron"
)

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		wantErr  bool
	}{
		{"every 5m", 300, false},
		{"every 30m", 1800, false},
		{"every 1h", 3600, false},
		{"every 2h", 7200, false},
		{"every day", 86400, false},
		{"daily", 86400, false},
		{"every hour", 3600, false},
		{"hourly", 3600, false},
		{"30m", 1800, false},
		{"2h", 7200, false},
		{"every 10 min", 600, false},
		{"every 3 hr", 10800, false},
		{"garbage", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := cron.ParseSchedule(tt.input)

			if tt.wantErr && err == nil {
				t.Errorf("expected error for %q", tt.input)
			}

			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.input, err)
			}

			if result != tt.expected {
				t.Errorf("input %q: expected %d, got %d", tt.input, tt.expected, result)
			}
		})
	}
}
