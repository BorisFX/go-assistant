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

// dwgTimeout bounds one conversion. A 16-sheet АР album is 8 MB of DWG and
// converts in tens of seconds; anything past this is a stuck process.
const dwgTimeout = 10 * time.Minute

// DWGExtractor reads CAD drawings through a fixed script instead of a model:
// coordinates, areas and stamp fields come out exactly as drawn, and a vision
// model misreading a digit in a tech plan is how a wrong legal conclusion gets
// written with full confidence.
type DWGExtractor struct {
	python string
	script string
}

// NewDWGExtractor wires the reader. Both paths must exist — an instance without
// the script simply does not get a CAD reader, and drawings fall back to vision.
func NewDWGExtractor(python, script string) (*DWGExtractor, error) {
	if python == "" || script == "" {
		return nil, fmt.Errorf("dwg: нужны путь к python и к скрипту")
	}
	if _, err := os.Stat(python); err != nil {
		return nil, fmt.Errorf("dwg: python %q: %w", python, err)
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("dwg: скрипт %q: %w", script, err)
	}
	return &DWGExtractor{python: python, script: script}, nil
}

// IsCAD reports whether the file is a drawing this extractor handles.
func IsCAD(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".dwg", ".dxf":
		return true
	default:
		return false
	}
}

// Extract returns the drawing as one page of text. One page, not many: a DWG is
// a single model space, and page numbers exist here only for citation.
func (d *DWGExtractor) Extract(ctx context.Context, path string) ([]output.PDFPage, error) {
	ctx, cancel := context.WithTimeout(ctx, dwgTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, d.python, d.script, path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, fmt.Errorf("чтение чертежа %s: %s", filepath.Base(path), reason)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return nil, fmt.Errorf("чертёж %s прочитан, но текста в нём не нашлось", filepath.Base(path))
	}
	return []output.PDFPage{{Number: 1, Text: text}}, nil
}

// WithCADExtractor routes .dwg/.dxf through the structural reader.
func WithCADExtractor(e *DWGExtractor) Option {
	return func(r *Router) { r.cad = e }
}

// extractCAD is the router's branch for drawings.
func (r *Router) extractCAD(ctx context.Context, path string) (Result, error) {
	pages, err := r.cad.Extract(ctx, path)
	if err != nil {
		return Result{}, err
	}
	slog.Info("legalreview extract", "path", path, "method", "dwg", "chars", totalChars(pages))
	return Result{Path: path, Method: "dwg", Pages: pages}, nil
}
