package telegram

import (
	"testing"
	"time"
)

func TestImageMemoryReturnsTheLastImageOfThatUser(t *testing.T) {
	now := time.Now()
	mem := newImageMemory(func() time.Time { return now })

	mem.set(1, "/files/first.jpg")
	mem.set(1, "/files/second.png")
	mem.set(2, "/files/other.jpg")

	if got := mem.get(1); got != "/files/second.png" {
		t.Errorf("get(1) = %q, want the most recent image", got)
	}
	if got := mem.get(2); got != "/files/other.jpg" {
		t.Errorf("get(2) = %q, want that user's own image", got)
	}
}

func TestImageMemoryHasNothingForAnUnknownUser(t *testing.T) {
	mem := newImageMemory(time.Now)

	if got := mem.get(42); got != "" {
		t.Errorf("get(42) = %q, want empty", got)
	}
}

// A photo from hours ago must not silently become the subject of "нарисуй кота".
func TestImageMemoryForgetsStaleImages(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	mem := newImageMemory(clock)

	mem.set(1, "/files/old.jpg")
	now = now.Add(imageMemoryTTL + time.Second)

	if got := mem.get(1); got != "" {
		t.Errorf("get(1) = %q, want empty after the TTL expired", got)
	}
}
