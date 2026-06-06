package legalreview

import (
	"strings"
	"testing"
)

func TestEstimateTokens_CharsOverFour(t *testing.T) {
	if got := estimateTokens(strings.Repeat("x", 400)); got != 100 {
		t.Fatalf("want 100 tokens for 400 chars, got %d", got)
	}
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("want 0 for empty, got %d", got)
	}
}

func TestFormatDigests_HeadersAndProvenance(t *testing.T) {
	body := formatDigests([]Digest{
		{Path: "/d/a.pdf", Method: "pdftotext", Text: "факт A (стр. 1)"},
		{Path: "/d/b.pdf", Method: "vision", Text: "факт B (стр. 2)"},
	})
	for _, want := range []string{"/d/a.pdf", "pdftotext", "факт A (стр. 1)", "/d/b.pdf", "vision", "факт B (стр. 2)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

// Непрочитанный документ обязан оставить ЯВНЫЙ след в теле, иначе координатор
// молча потеряет юр-вывод по этому документу (дизайн: «не прочитан»).
func TestFormatDigests_EmptyTextMarkedUnread(t *testing.T) {
	body := formatDigests([]Digest{{Path: "/d/x.pdf", Text: "   "}})
	if !strings.Contains(body, "/d/x.pdf") || !strings.Contains(body, "не прочитан") {
		t.Fatalf("empty digest must be marked unread, got:\n%s", body)
	}
}
