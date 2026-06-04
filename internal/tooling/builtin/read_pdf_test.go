package builtin

import (
	"strings"
	"testing"
)

func TestSliceTextWindow(t *testing.T) {
	chunk, total, start, truncated := sliceText("abcdefghij", 2, 3)
	if chunk != "cde" || total != 10 || start != 2 || !truncated {
		t.Errorf("got chunk=%q total=%d start=%d trunc=%v", chunk, total, start, truncated)
	}
}

func TestSliceTextOffsetBeyondEnd(t *testing.T) {
	chunk, _, start, truncated := sliceText("abc", 99, 10)
	if chunk != "" || start != 3 || truncated {
		t.Errorf("offset past end: chunk=%q start=%d trunc=%v", chunk, start, truncated)
	}
}

func TestSliceTextDefaultsAndClamp(t *testing.T) {
	text := strings.Repeat("я", 50000) // multi-byte runes
	_, total, _, truncated := sliceText(text, 0, 0)
	if total != 50000 || !truncated {
		t.Errorf("expected rune-count total=50000 truncated, got total=%d trunc=%v", total, truncated)
	}
	chunk, _, _, _ := sliceText(text, 0, 999999)
	if len([]rune(chunk)) != 40000 {
		t.Errorf("max_chars should clamp to 40000, got %d", len([]rune(chunk)))
	}
}

func TestSliceTextNegativeOffset(t *testing.T) {
	chunk, _, start, _ := sliceText("hello", -5, 2)
	if start != 0 || chunk != "he" {
		t.Errorf("negative offset should clamp to 0: start=%d chunk=%q", start, chunk)
	}
}
