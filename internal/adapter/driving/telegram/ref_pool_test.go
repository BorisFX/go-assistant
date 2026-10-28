package telegram

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestRefPoolKeepsOrderAndSplitsKinds(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.add(1, "/a.jpg", false)
	pool.add(1, "/gif.mp4", true)
	pool.add(1, "/b.jpg", false)

	refs := pool.refs(1)
	if len(refs.Images) != 2 || refs.Images[0] != "/a.jpg" || refs.Images[1] != "/b.jpg" {
		t.Errorf("images = %v", refs.Images)
	}
	if len(refs.Videos) != 1 || refs.Videos[0] != "/gif.mp4" {
		t.Errorf("videos = %v", refs.Videos)
	}
	if other := pool.refs(2); !other.Empty() {
		t.Errorf("another user sees %v", other)
	}
}

func TestRefPoolStartsNewSetAfterGap(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.add(1, "/old.jpg", false)
	clock.advance(refBatchGap + time.Minute)
	pool.add(1, "/new.jpg", false)

	if refs := pool.refs(1); len(refs.Images) != 1 || refs.Images[0] != "/new.jpg" {
		t.Errorf("images = %v, want only the new set", refs.Images)
	}
}

func TestRefPoolExpires(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.add(1, "/a.jpg", false)
	clock.advance(refPoolTTL + time.Second)

	if refs := pool.refs(1); !refs.Empty() {
		t.Errorf("refs = %v, want expired", refs)
	}
}

func TestRefPoolShowsEachImageOnce(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.add(1, "/a.jpg", false)
	pool.add(1, "/gif.mp4", true)
	pool.add(1, "/b.jpg", false)

	if got := pool.takeUnshown(1); len(got) != 2 {
		t.Fatalf("first take = %v, want both images", got)
	}
	if got := pool.takeUnshown(1); len(got) != 0 {
		t.Errorf("second take = %v, want nothing", got)
	}
	// Shown images still count as references.
	if refs := pool.refs(1); len(refs.Images) != 2 {
		t.Errorf("refs = %v, want shown images kept", refs.Images)
	}
}

func TestRefPoolCapsSize(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)
	for range refPoolMax + 3 {
		pool.add(1, "/x.jpg", false)
	}
	if n, _ := pool.count(1); n != refPoolMax {
		t.Errorf("count = %d, want %d", n, refPoolMax)
	}
}

func TestRefPoolStartsNewSetAfterGeneration(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.add(1, "/note.mp4", true)
	pool.markUsed(1)
	// The used set stays available for "same, but longer".
	if refs := pool.refs(1); len(refs.Videos) != 1 {
		t.Fatalf("videos = %v, want the used set kept", refs.Videos)
	}

	pool.add(1, "/gif.mp4", true)
	pool.add(1, "/photo.jpg", false)
	refs := pool.refs(1)
	if len(refs.Videos) != 1 || refs.Videos[0] != "/gif.mp4" || len(refs.Images) != 1 {
		t.Errorf("refs = %+v, want only the new gif and photo", refs)
	}
}

func TestRefPoolInventoryNamesFramesAndClips(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	pool := newRefPool(clock.now)

	pool.addItem(1, refItem{path: "/note.mp4", video: true, kind: "round video note of the user"})
	pool.addItem(1, refItem{path: "/note_still.jpg", kind: "photo", frameOf: "/note.mp4"})
	pool.add(1, "/cat.jpg", false)

	want := "[Image 1] = still frame of the round video note of the user; [Image 2] = photo"
	if got := pool.inventory(1); got != want {
		t.Errorf("inventory =\n%s\nwant\n%s", got, want)
	}
}

func TestRefPoolSurvivesRestart(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	file := t.TempDir() + "/pool.json"

	pool := newRefPool(clock.now).persistTo(file)
	pool.addItem(1, refItem{path: "/note.mp4", video: true, kind: "GIF"})
	pool.addItem(1, refItem{path: "/note_still.jpg", kind: "photo", frameOf: "/note.mp4"})
	pool.takeUnshown(1)
	pool.markUsed(1)

	restored := newRefPool(clock.now).persistTo(file)
	if got := restored.inventory(1); got != "[Image 1] = still frame of the GIF" {
		t.Errorf("inventory after restart = %q", got)
	}
	if got := restored.takeUnshown(1); len(got) != 0 {
		t.Errorf("shown flags lost: %v", got)
	}
	restored.add(1, "/new.jpg", false)
	if refs := restored.refs(1); len(refs.Images) != 1 || refs.Images[0] != "/new.jpg" {
		t.Errorf("used flag lost: %v", refs.Images)
	}
}
