package output

import "context"

// PDFPage is one extracted chunk of a document. Number is a 1-based ordinal
// kept so downstream digests can cite "файл X, стр. N". For pdftotext it is the
// physical page number; for OCR/vision engines it may be a chunk index, since
// those engines do not always report physical pages.
type PDFPage struct {
	Number int
	Text   string
}

// RemotePDFExtractor extracts text from a PDF using a server-side engine.
//
// ExtractText runs a file-parser OCR engine (e.g. "mistral-ocr",
// "cloudflare-ai") and returns the parsed text per page from the response
// annotations. ReadVision sends the PDF to a vision model and returns the
// model's reading (text + stamps off drawings), not a structural OCR.
type RemotePDFExtractor interface {
	ExtractText(ctx context.Context, path, engine string) ([]PDFPage, error)
	ReadVision(ctx context.Context, path, model, prompt string) ([]PDFPage, error)
}
