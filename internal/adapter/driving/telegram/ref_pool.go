package telegram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

// How long sent pictures stay usable as references.
const refPoolTTL = 30 * time.Minute

// A picture sent after this much silence starts a new reference set, so photos
// from an earlier idea don't leak into the next clip.
const refBatchGap = 10 * time.Minute

// Upper bound on what one user can stash; older items drop off first.
const refPoolMax = 12

type refItem struct {
	path  string
	video bool
	// kind is how the item is described to the model: "photo", "GIF", ...
	kind string
	// frameOf is the clip a still frame was cut from, so the model knows
	// [Image N] and [Video M] show the same subject.
	frameOf string
	at      time.Time
	// shown marks images already attached to an LLM request, so each picture
	// is looked at once rather than resent with every later message.
	shown bool
}

// refPool keeps the pictures and clips each user recently sent, in order —
// the references a video (or edit) is built from. Albums and photos sent one
// by one both land here, which is what lets "make a video from these" see all
// of them. Saved to disk on every change, so a deploy doesn't make the user
// send everything again.
type refPool struct {
	mu    sync.Mutex
	now   func() time.Time
	file  string // where the pool survives restarts; empty keeps it in memory
	items map[int64][]refItem
	// used marks users whose current set already fed a generation: it stays
	// available for retries, but the next item sent starts a new set.
	used map[int64]bool
}

func newRefPool(now func() time.Time) *refPool {
	return &refPool{now: now, items: make(map[int64][]refItem), used: make(map[int64]bool)}
}

func (p *refPool) add(userID int64, path string, video bool) {
	kind := "photo"
	if video {
		kind = "video"
	}
	p.addItem(userID, refItem{path: path, video: video, kind: kind})
}

func (p *refPool) addItem(userID int64, item refItem) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	items := p.fresh(userID)
	if n := len(items); p.used[userID] || (n > 0 && now.Sub(items[n-1].at) > refBatchGap) {
		items = nil
	}
	delete(p.used, userID)
	item.at = now
	items = append(items, item)
	if len(items) > refPoolMax {
		items = items[len(items)-refPoolMax:]
	}
	p.items[userID] = items
	p.save()
}

// refs returns every live reference, oldest first.
func (p *refPool) refs(userID int64) imgctx.Refs {
	p.mu.Lock()
	defer p.mu.Unlock()

	var refs imgctx.Refs
	for _, it := range p.fresh(userID) {
		if it.video {
			refs.Videos = append(refs.Videos, it.path)
		} else {
			refs.Images = append(refs.Images, it.path)
		}
	}
	return refs
}

// inventory describes the live pictures the way prompts must cite them — the
// model writing the video prompt otherwise guesses "[Image 1]" for things that
// aren't there. Clips themselves can't be used, only their still frames.
func (p *refPool) inventory(userID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	items := p.fresh(userID)
	kinds := make(map[string]string)
	for _, it := range items {
		if it.video {
			kinds[it.path] = it.kind
		}
	}
	var images []string
	for _, it := range items {
		if it.video {
			continue
		}
		desc := it.kind
		if kind, ok := kinds[it.frameOf]; ok {
			desc = "still frame of the " + kind
		}
		images = append(images, fmt.Sprintf("[Image %d] = %s", len(images)+1, desc))
	}
	return strings.Join(images, "; ")
}

// takeUnshown returns the images the model hasn't seen yet and marks them seen.
func (p *refPool) takeUnshown(userID int64) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	items := p.fresh(userID)
	var paths []string
	for i := range items {
		if !items[i].video && !items[i].shown {
			items[i].shown = true
			paths = append(paths, items[i].path)
		}
	}
	if len(paths) > 0 {
		p.save()
	}
	return paths
}

// count is what the user sent: still frames cut from clips are not counted.
func (p *refPool) count(userID int64) (images, videos int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, it := range p.fresh(userID) {
		switch {
		case it.video:
			videos++
		case it.frameOf == "":
			images++
		}
	}
	return images, videos
}

// markUsed records that a generation consumed the current set.
func (p *refPool) markUsed(userID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.used[userID] = true
	p.save()
}

func (p *refPool) clear(userID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, userID)
	delete(p.used, userID)
	p.save()
}

// fresh drops expired items; callers hold the lock.
func (p *refPool) fresh(userID int64) []refItem {
	items := p.items[userID]
	cutoff := p.now().Add(-refPoolTTL)
	i := 0
	for i < len(items) && items[i].at.Before(cutoff) {
		i++
	}
	items = items[i:]
	if len(items) == 0 {
		delete(p.items, userID)
		return nil
	}
	p.items[userID] = items
	return items
}

type savedItem struct {
	Path    string    `json:"path"`
	Video   bool      `json:"video"`
	Kind    string    `json:"kind"`
	FrameOf string    `json:"frame_of,omitempty"`
	At      time.Time `json:"at"`
	Shown   bool      `json:"shown"`
}

type savedPool struct {
	Items map[int64][]savedItem `json:"items"`
	Used  map[int64]bool        `json:"used"`
}

// persistTo loads the pool from file and keeps saving it there.
func (p *refPool) persistTo(file string) *refPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.file = file

	raw, err := os.ReadFile(file)
	if err != nil {
		return p
	}
	var saved savedPool
	if err := json.Unmarshal(raw, &saved); err != nil {
		slog.Warn("reference pool file unreadable, starting empty", "file", file, "error", err)
		return p
	}
	for userID, list := range saved.Items {
		for _, it := range list {
			p.items[userID] = append(p.items[userID], refItem{
				path: it.Path, video: it.Video, kind: it.Kind, frameOf: it.FrameOf, at: it.At, shown: it.Shown,
			})
		}
	}
	maps.Copy(p.used, saved.Used)
	return p
}

// save writes the pool; callers hold the lock. Failures only cost persistence.
func (p *refPool) save() {
	if p.file == "" {
		return
	}
	saved := savedPool{Items: make(map[int64][]savedItem, len(p.items)), Used: p.used}
	for userID, list := range p.items {
		for _, it := range list {
			saved.Items[userID] = append(saved.Items[userID], savedItem{
				Path: it.path, Video: it.video, Kind: it.kind, FrameOf: it.frameOf, At: it.at, Shown: it.shown,
			})
		}
	}
	raw, err := json.Marshal(saved)
	if err == nil {
		tmp := p.file + ".tmp"
		if err = os.WriteFile(tmp, raw, 0644); err == nil {
			err = os.Rename(tmp, p.file)
		}
	}
	if err != nil {
		slog.Warn("could not save reference pool", "file", p.file, "error", err)
	}
}
