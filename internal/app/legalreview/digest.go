package legalreview

import (
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// chunkPages groups consecutive pages into batches whose combined text stays
// under maxChars. A single page larger than the budget gets its own batch (we
// don't split below page granularity). Returns nil for no pages.
func chunkPages(pages []output.PDFPage, maxChars int) [][]output.PDFPage {
	if len(pages) == 0 {
		return nil
	}
	var (
		batches [][]output.PDFPage
		cur     []output.PDFPage
		curLen  int
	)
	for _, p := range pages {
		n := len(p.Text)
		if len(cur) > 0 && curLen+n > maxChars {
			batches = append(batches, cur)
			cur, curLen = nil, 0
		}
		cur = append(cur, p)
		curLen += n
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}
