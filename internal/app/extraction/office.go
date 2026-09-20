package extraction

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// officeTimeout bounds one document. LibreOffice's first run warms up for a few
// seconds; a minute-scale estimate with headroom keeps a stuck conversion from
// holding a review batch.
const officeTimeout = 6 * time.Minute

// OfficeExtractor reads contracts and estimates — the formats the review list
// claimed to accept while pdftotext quietly failed on every one of them.
type OfficeExtractor struct {
	python string
	script string
}

func NewOfficeExtractor(python, script string) (*OfficeExtractor, error) {
	if python == "" || script == "" {
		return nil, fmt.Errorf("office: нужны путь к python и к скрипту")
	}
	if _, err := os.Stat(python); err != nil {
		return nil, fmt.Errorf("office: python %q: %w", python, err)
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("office: скрипт %q: %w", script, err)
	}
	return &OfficeExtractor{python: python, script: script}, nil
}

// IsOffice reports whether the file is an office document.
func IsOffice(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".doc", ".docx", ".xls", ".xlsx", ".xlsm", ".rtf", ".odt", ".ods":
		return true
	default:
		return false
	}
}

// Extract returns the document as text. Spreadsheets come back sheet by sheet
// with tab-separated cells, so an estimate keeps its columns.
func (o *OfficeExtractor) Extract(ctx context.Context, path string) ([]output.PDFPage, error) {
	ctx, cancel := context.WithTimeout(ctx, officeTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, o.python, o.script, path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, fmt.Errorf("чтение документа %s: %s", filepath.Base(path), reason)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return nil, fmt.Errorf("документ %s пуст", filepath.Base(path))
	}
	return []output.PDFPage{{Number: 1, Text: text}}, nil
}

// WithOfficeExtractor routes office documents through it.
func WithOfficeExtractor(e *OfficeExtractor) Option {
	return func(r *Router) { r.office = e }
}

func (r *Router) extractOffice(ctx context.Context, path string) (Result, error) {
	pages, err := r.office.Extract(ctx, path)
	if err != nil {
		return Result{}, err
	}
	slog.Info("legalreview extract", "path", path, "method", "office", "chars", totalChars(pages))
	return Result{Path: path, Method: "office", Pages: pages}, nil
}
