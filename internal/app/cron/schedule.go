package cron

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var clockTimeRe = regexp.MustCompile(`(?i)^(?:every\s+day\s+at|daily\s+at|at)\s+(\d{1,2}):(\d{2})$`)

// ComputeNext returns the next fire time for a schedule string, the interval in
// seconds (for storage/display), and an error if the schedule can't be parsed.
//
// Two families are supported:
//   - intervals: "every 30m", "every 2h", "hourly", "daily", "45m" — fire
//     interval seconds after `from`.
//   - clock-time: "daily at 09:00", "every day at 18:30", "at 07:00" — fire at
//     the next occurrence of HH:MM in `loc` strictly after `from`.
//
// A nil loc is treated as UTC.
func ComputeNext(schedule string, from time.Time, loc *time.Location) (time.Time, int, error) {
	if loc == nil {
		loc = time.UTC
	}

	if m := clockTimeRe.FindStringSubmatch(schedule); m != nil {
		hh, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if hh > 23 || mm > 59 {
			return time.Time{}, 0, fmt.Errorf("invalid time %02d:%02d in schedule %q", hh, mm, schedule)
		}
		local := from.In(loc)
		next := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
		if !next.After(from) {
			next = next.AddDate(0, 0, 1)
		}
		return next, 86400, nil
	}

	sec, err := ParseSchedule(schedule)
	if err != nil {
		return time.Time{}, 0, err
	}
	return from.Add(time.Duration(sec) * time.Second), sec, nil
}
