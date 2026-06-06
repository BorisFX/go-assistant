package extraction

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// LocalExtractor pulls embedded text out of a PDF with pdftotext. It is the
// free first stage: documents that already carry a text layer never reach a
// paid OCR/vision engine. The run func is injectable so page-split logic is
// unit-testable without invoking the binary.
type LocalExtractor struct {
	run func(ctx context.Context, path string) (string, error)
}

func NewLocalExtractor() *LocalExtractor {
	return &LocalExtractor{run: runPdftotext}
}

// Extract returns the document's text pages, or nil pages (no error) when the
// PDF has no embedded text — the signal for the Router to escalate.
func (e *LocalExtractor) Extract(ctx context.Context, path string) ([]output.PDFPage, error) {
	raw, err := e.run(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("pdftotext: %w", err)
	}
	return splitPages(raw), nil
}

func runPdftotext(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-enc", "UTF-8", path, "-")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// splitPages splits pdftotext's form-feed-delimited output into pages, keeping
// physical 1-based page numbers (blank pages are skipped but still counted).
// Returns nil when there is no extractable text.
func splitPages(raw string) []output.PDFPage {
	var pages []output.PDFPage
	for i, part := range strings.Split(raw, "\f") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		pages = append(pages, output.PDFPage{Number: i + 1, Text: part})
	}
	return pages
}
