package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ReadPDF extracts plain text from a PDF on the server so the model can read a
// document's real content instead of judging it by file name.
type ReadPDF struct{}

func NewReadPDF() *ReadPDF { return &ReadPDF{} }

func (r *ReadPDF) Name() string { return "read_pdf" }

func (r *ReadPDF) Description() string {
	return "Extract text from a PDF file on the server, preserving the page layout so table rows (экспликации, штампы, ТЭП) stay on one line with their numbers and areas. ALWAYS read a document's actual text with this before stating anything about its content (e.g. whether a section, legend, value or field is present). Never judge a PDF by its file name, and never quote a figure from memory — re-read the file. Multi-sheet albums are read sheet by sheet with first_page/last_page; long text is paged through with offset."
}

func (r *ReadPDF) Category() string { return "documents" }

func (r *ReadPDF) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Local server path to a .pdf file (download or unzip it first if needed)"},
			"first_page": {"type": "integer", "description": "First page to read (1-based). Omit to read from the start."},
			"last_page": {"type": "integer", "description": "Last page to read (1-based). Omit to read to the end."},
			"offset": {"type": "integer", "description": "Character offset to start from (default 0). Use to page through long documents.", "default": 0},
			"max_chars": {"type": "integer", "description": "Max characters to return (default 12000, max 40000)", "default": 12000}
		},
		"required": ["path"]
	}`)
}

type readPDFParams struct {
	Path      string `json:"path"`
	FirstPage int    `json:"first_page"`
	LastPage  int    `json:"last_page"`
	Offset    int    `json:"offset"`
	MaxChars  int    `json:"max_chars"`
}

func (r *ReadPDF) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p readPDFParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if _, err := os.Stat(p.Path); err != nil {
		return nil, fmt.Errorf("file not found: %s", p.Path)
	}

	cmd := exec.CommandContext(ctx, "pdftotext", pdftotextArgs(p.Path, p.FirstPage, p.LastPage)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext failed: %v: %s", err, strings.TrimSpace(errb.String()))
	}

	chunk, total, start, truncated := sliceText(out.String(), p.Offset, p.MaxChars)

	result := map[string]any{
		"path":        p.Path,
		"pages":       pageRangeLabel(p.FirstPage, p.LastPage),
		"total_chars": total,
		"offset":      start,
		"returned":    len([]rune(chunk)),
		"truncated":   truncated,
		"text":        chunk,
	}
	if strings.TrimSpace(chunk) == "" && total == 0 {
		result["warning"] = "No extractable text — the PDF is likely a scanned image (OCR is not available)."
	}
	return json.Marshal(result)
}

// pdftotextArgs builds the extraction command. -layout keeps columns aligned:
// a drawing's экспликация is emitted column by column, so without it a table
// arrives as separate lists of numbers, names and areas that can only be
// re-paired by guessing — and a wrong guess reads as a confident fact.
func pdftotextArgs(path string, firstPage, lastPage int) []string {
	args := []string{"-layout", "-enc", "UTF-8"}
	if firstPage > 0 {
		args = append(args, "-f", strconv.Itoa(firstPage))
	}
	if lastPage > 0 {
		args = append(args, "-l", strconv.Itoa(lastPage))
	}
	return append(args, path, "-")
}

// pageRangeLabel tells the model which sheets it is actually looking at, so a
// partial read is never mistaken for the whole album.
func pageRangeLabel(firstPage, lastPage int) string {
	switch {
	case firstPage <= 0 && lastPage <= 0:
		return "all"
	case firstPage <= 0:
		return fmt.Sprintf("1-%d", lastPage)
	case lastPage <= 0:
		return fmt.Sprintf("%d-end", firstPage)
	default:
		return fmt.Sprintf("%d-%d", firstPage, lastPage)
	}
}

// sliceText returns a rune-safe window of text starting at offset, clamping the
// bounds and the requested length to sane limits.
func sliceText(text string, offset, maxChars int) (chunk string, total, start int, truncated bool) {
	if maxChars <= 0 {
		maxChars = 12000
	}
	if maxChars > 40000 {
		maxChars = 40000
	}
	if offset < 0 {
		offset = 0
	}

	runes := []rune(text)
	total = len(runes)
	start = min(offset, total)
	end := min(start+maxChars, total)
	return string(runes[start:end]), total, start, end < total
}
