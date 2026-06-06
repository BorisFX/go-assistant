package output

import "context"

// PDFPage is one extracted page of a document. Number is the 1-based physical
// page number, preserved so downstream digests can cite "файл X, стр. N".
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
