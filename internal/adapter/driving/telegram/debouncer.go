package telegram

import (
	"sync"
	"time"
)

type Debouncer struct {
	mu       sync.Mutex
	perChat  map[int64]time.Time
	minDelay time.Duration
}

func NewDebouncer(minDelay time.Duration) *Debouncer {
	return &Debouncer{
		perChat:  make(map[int64]time.Time),
		minDelay: minDelay,
	}
}

func (d *Debouncer) Allow(chatID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	last, exists := d.perChat[chatID]
	if exists && time.Since(last) < d.minDelay {
		return false
	}

	d.perChat[chatID] = time.Now()
	return true
}
