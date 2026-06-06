package legalreview

import (
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func TestChunkPages_SingleBatchWhenUnderBudget(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: "aaaa"},
		{Number: 2, Text: "bbbb"},
	}
	batches := chunkPages(pages, 1000)
	if len(batches) != 1 {
		t.Fatalf("want 1 batch, got %d", len(batches))
	}
	if len(batches[0]) != 2 {
		t.Fatalf("want 2 pages in batch, got %d", len(batches[0]))
	}
}

func TestChunkPages_SplitsWhenOverBudget(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: strings.Repeat("a", 6)},
		{Number: 2, Text: strings.Repeat("b", 6)},
		{Number: 3, Text: strings.Repeat("c", 6)},
	}
	// Budget 10 chars: each page is 6, so two pages (12) overflow -> 1 page per batch.
	batches := chunkPages(pages, 10)
	if len(batches) != 3 {
		t.Fatalf("want 3 batches, got %d", len(batches))
	}
}

func TestChunkPages_OversizedSinglePageGetsOwnBatch(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: strings.Repeat("x", 50)},
		{Number: 2, Text: "y"},
	}
	batches := chunkPages(pages, 10)
	if len(batches) != 2 {
		t.Fatalf("want 2 batches, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].Number != 1 {
		t.Fatalf("oversized page must be alone in its batch: %+v", batches[0])
	}
}

func TestChunkPages_EmptyIsNil(t *testing.T) {
	if b := chunkPages(nil, 100); b != nil {
		t.Fatalf("want nil for no pages, got %+v", b)
	}
}
