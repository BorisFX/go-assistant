package telegram

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type PollingWatchdog struct {
	timeout    time.Duration
	lastUpdate atomic.Int64
	onRestart  func()
}

func NewPollingWatchdog(timeout time.Duration, onRestart func()) *PollingWatchdog {
	w := &PollingWatchdog{
		timeout:   timeout,
		onRestart: onRestart,
	}
	w.Touch()
	return w
}

func (w *PollingWatchdog) Touch() {
	w.lastUpdate.Store(time.Now().Unix())
}

func (w *PollingWatchdog) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(w.lastUpdate.Load(), 0)
			if time.Since(last) > w.timeout {
				slog.Warn("polling watchdog triggered", "last_update", last, "timeout", w.timeout)
				w.Touch()
				if w.onRestart != nil {
					w.onRestart()
				}
			}
		}
	}
}
