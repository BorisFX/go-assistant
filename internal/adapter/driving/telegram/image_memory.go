package telegram

import (
	"sync"
	"time"
)

// How long the last picture of a chat stays the default subject of an edit.
// Long enough to iterate on a photo, short enough that yesterday's picture
// doesn't hijack today's "draw me a cat".
const imageMemoryTTL = 30 * time.Minute

type rememberedImage struct {
	path string
	at   time.Time
}

// imageMemory keeps the last picture each user sent or received, so follow-up
// edits ("now make the hat red") work without re-uploading it. In-memory by
// design: a bot restart simply asks for the photo again.
type imageMemory struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[int64]rememberedImage
}

func newImageMemory(now func() time.Time) *imageMemory {
	return &imageMemory{now: now, items: make(map[int64]rememberedImage)}
}

func (m *imageMemory) set(userID int64, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[userID] = rememberedImage{path: path, at: m.now()}
}

func (m *imageMemory) get(userID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.items[userID]
	if !ok {
		return ""
	}
	if m.now().Sub(item.at) > imageMemoryTTL {
		delete(m.items, userID)
		return ""
	}
	return item.path
}
