package legalreview

import (
	"strings"
	"testing"
)

func TestRenderContextKeepsRecentTurnsOldestFirst(t *testing.T) {
	var turns []ChatTurn
	for i := 0; i < 20; i++ {
		turns = append(turns, ChatTurn{Role: "user", Text: "вопрос " + string(rune('a'+i))})
	}
	out := RenderContext(turns)
	if strings.Contains(out, "вопрос a") {
		t.Fatalf("oldest turns must be dropped past the limit:\n%s", out)
	}
	if !strings.Contains(out, "вопрос t") {
		t.Fatalf("newest turn missing:\n%s", out)
	}
	if strings.Index(out, "вопрос s") > strings.Index(out, "вопрос t") {
		t.Fatalf("turns must be chronological:\n%s", out)
	}
	if strings.Count(out, "Пользователь:") != contextMaxTurns {
		t.Fatalf("want %d turns, got %d", contextMaxTurns, strings.Count(out, "Пользователь:"))
	}
}

func TestRenderContextTruncatesLongTurnsAndSkipsEmpty(t *testing.T) {
	long := strings.Repeat("ѣ", contextTurnChars+500)
	out := RenderContext([]ChatTurn{
		{Role: "assistant", Text: long},
		{Role: "user", Text: "   "},
		{Role: "tool", Text: "raw tool output"},
		{Role: "user", Text: "а этажность?"},
	})
	if strings.Count(out, "ѣ") != contextTurnChars {
		t.Fatalf("long turn not truncated to %d runes", contextTurnChars)
	}
	if !strings.Contains(out, "…") {
		t.Fatal("truncation must be visible")
	}
	if strings.Contains(out, "raw tool output") {
		t.Fatal("tool turns are not conversation")
	}
	if !strings.Contains(out, "Ассистент:") || !strings.Contains(out, "а этажность?") {
		t.Fatalf("roles and last question expected:\n%s", out)
	}
}

func TestRenderContextEmpty(t *testing.T) {
	if RenderContext(nil) != "" || RenderContext([]ChatTurn{{Role: "user", Text: ""}}) != "" {
		t.Fatal("no turns must render to an empty string")
	}
}
