package extraction

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// Engine names for the OpenRouter file-parser. "pdf-text" is intentionally
// absent: it is deprecated and redirects to "cloudflare-ai".
const (
	EngineMistralOCR = "mistral-ocr"
	EngineCloudflare = "cloudflare-ai"
)

// defaultVisionPrompt instructs the vision model to read data OFF a drawing
// (text + stamps), not to interpret the engineering. The compliance verdict
// (СП/СанПиН) is the coordinator's job, not the extractor's.
const defaultVisionPrompt = "Считай с этого чертежа/техплана ВСЕ текстовые данные и штампы: координаты, площади, материалы, этажность, отметки, подписи штампов, даты. Не интерпретируй инженерные решения и не делай выводов о соответствии нормам — только дословно перечисли что написано на листе."

type localExtractor interface {
	Extract(ctx context.Context, path string) ([]output.PDFPage, error)
}

// Result is the outcome of routing one document through the extraction layer.
type Result struct {
	Path   string
	Method string // "pdftotext" | "vision" | "mistral-ocr"
	Pages  []output.PDFPage
}

// Router selects an extraction strategy per document: free local text first,
// then vision (drawings) or OCR (dense scans) when there is no text layer.
type Router struct {
	local        localExtractor
	remote       output.RemotePDFExtractor
	visionModel  string
	visionPrompt string
	denseScan    func(path string) bool
}

// Option configures a Router.
type Option func(*Router)

func WithLocal(l localExtractor) Option { return func(r *Router) { r.local = l } }

func WithVisionModel(model string) Option { return func(r *Router) { r.visionModel = model } }

func WithVisionPrompt(p string) Option { return func(r *Router) { r.visionPrompt = p } }

// WithDenseScanDetector decides whether a text-less PDF is a dense text scan
// (→ OCR) rather than a drawing (→ vision). Default: always treat as a drawing.
func WithDenseScanDetector(f func(path string) bool) Option {
	return func(r *Router) { r.denseScan = f }
}

func NewRouter(remote output.RemotePDFExtractor, opts ...Option) *Router {
	r := &Router{
		local:        NewLocalExtractor(),
		remote:       remote,
		visionPrompt: defaultVisionPrompt,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Extract routes one PDF to the cheapest engine that can read it and returns
// its pages with the method used. Page numbers are preserved for citation.
func (r *Router) Extract(ctx context.Context, path string) (Result, error) {
	// XML/plain-text documents (GKUOKS tech plans are XML) carry their content
	// directly — no PDF text layer to mine, no OCR/vision to escalate to.
	if isTextExt(path) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Result{}, fmt.Errorf("read text %q: %w", path, err)
		}
		pages := []output.PDFPage{{Number: 1, Text: string(raw)}}
		slog.Info("legalreview extract", "path", path, "method", "text", "pages", 1, "chars", len(raw))
		return Result{Path: path, Method: "text", Pages: pages}, nil
	}

	pages, err := r.local.Extract(ctx, path)
	if err != nil {
		return Result{}, fmt.Errorf("local extract %q: %w", path, err)
	}
	if len(pages) > 0 {
		slog.Info("legalreview extract", "path", path, "method", "pdftotext", "pages", len(pages), "chars", totalChars(pages))
		return Result{Path: path, Method: "pdftotext", Pages: pages}, nil
	}

	// No embedded text — a scanned page or a drawing.
	if r.denseScan != nil && r.denseScan(path) {
		ocr, err := r.remote.ExtractText(ctx, path, EngineMistralOCR)
		if err != nil {
			return Result{}, fmt.Errorf("ocr extract %q: %w", path, err)
		}
		slog.Info("legalreview extract", "path", path, "method", EngineMistralOCR, "pages", len(ocr), "chars", totalChars(ocr))
		return Result{Path: path, Method: EngineMistralOCR, Pages: ocr}, nil
	}

	vis, err := r.remote.ReadVision(ctx, path, r.visionModel, r.visionPrompt)
	if err != nil {
		return Result{}, fmt.Errorf("vision extract %q: %w", path, err)
	}
	slog.Info("legalreview extract", "path", path, "method", "vision", "model", r.visionModel, "pages", len(vis), "chars", totalChars(vis))
	return Result{Path: path, Method: "vision", Pages: vis}, nil
}

// isTextExt reports whether path is a directly-readable text document (XML tech
// plans, plain text) that needs no PDF text-layer, OCR, or vision stage.
func isTextExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xml", ".txt":
		return true
	default:
		return false
	}
}

// totalChars sums the text length across pages for extraction logging.
func totalChars(pages []output.PDFPage) int {
	n := 0
	for _, p := range pages {
		n += len(p.Text)
	}
	return n
}
