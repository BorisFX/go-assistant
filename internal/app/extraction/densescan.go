package extraction

import (
	"context"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// pdfinfoTimeout bounds one metadata read; pdfinfo answers in milliseconds.
const pdfinfoTimeout = 30 * time.Second

// drawingMinPts is the sheet dimension above which a text-less PDF is treated
// as a drawing. A4 is 595×842 pt and Letter 612×792 pt; A3 (842×1191 pt) and
// larger sheets are what drawings are printed on.
const drawingMinPts = 1000

var rePageSize = regexp.MustCompile(`(?m)^Page size:\s*([\d.]+)\s*x\s*([\d.]+)`)

// commandRunner runs a command and returns its stdout; injected so tests never
// shell out.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// DenseScanByPageSize decides whether a text-less PDF is a scanned text
// document (→ OCR) or a drawing (→ vision) from its page size alone. A scanned
// suspension order or contract is A4; a drawing is A3 or larger. Any failure to
// read the metadata keeps today's behaviour (vision).
func DenseScanByPageSize(pdfinfo string) func(path string) bool {
	return denseScanWith(pdfinfo, execRunner)
}

func denseScanWith(pdfinfo string, run commandRunner) func(path string) bool {
	return func(path string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), pdfinfoTimeout)
		defer cancel()

		out, err := run(ctx, pdfinfo, path)
		if err != nil {
			slog.Warn("legalreview densescan: pdfinfo failed, defaulting to vision", "path", path, "error", err)
			return false
		}
		w, h, ok := parsePageSize(string(out))
		if !ok {
			slog.Warn("legalreview densescan: no page size in pdfinfo output, defaulting to vision", "path", path)
			return false
		}
		dense := max(w, h) <= drawingMinPts
		slog.Info("legalreview densescan", "path", path, "width_pt", w, "height_pt", h, "dense_scan", dense)
		return dense
	}
}

// parsePageSize reads the first "Page size: W x H pts" line of pdfinfo output.
func parsePageSize(out string) (w, h float64, ok bool) {
	m := rePageSize.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	w, errW := strconv.ParseFloat(m[1], 64)
	h, errH := strconv.ParseFloat(m[2], 64)
	if errW != nil || errH != nil {
		return 0, 0, false
	}
	return w, h, true
}
