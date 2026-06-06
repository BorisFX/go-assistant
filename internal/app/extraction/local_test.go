package extraction

import (
	"context"
	"testing"
)

func TestSplitPages_FormFeedDelimited(t *testing.T) {
	raw := "page one\fpage two\fpage three"
	pages := splitPages(raw)
	if len(pages) != 3 {
		t.Fatalf("want 3 pages, got %d", len(pages))
	}
	if pages[0].Number != 1 || pages[2].Number != 3 {
		t.Fatalf("page numbering wrong: %+v", pages)
	}
}

func TestSplitPages_PreservesPhysicalNumbersAcrossBlankPages(t *testing.T) {
	// Page 2 is blank (scanned divider); numbering must stay 1 and 3.
	raw := "first\f   \fthird"
	pages := splitPages(raw)
	if len(pages) != 2 {
		t.Fatalf("want 2 non-empty pages, got %d", len(pages))
	}
	if pages[0].Number != 1 || pages[1].Number != 3 {
		t.Fatalf("want physical numbers 1 and 3, got %d and %d", pages[0].Number, pages[1].Number)
	}
}

func TestSplitPages_EmptyIsNil(t *testing.T) {
	if pages := splitPages(""); pages != nil {
		t.Fatalf("want nil for empty text, got %+v", pages)
	}
	if pages := splitPages("   \n\f  \f"); pages != nil {
		t.Fatalf("want nil for whitespace-only text, got %+v", pages)
	}
}

func TestLocalExtractor_UsesInjectedRunner(t *testing.T) {
	e := &LocalExtractor{run: func(_ context.Context, _ string) (string, error) {
		return "alpha\fbeta", nil
	}}
	pages, err := e.Extract(context.Background(), "/tmp/x.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(pages) != 2 || pages[1].Text != "beta" {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}
