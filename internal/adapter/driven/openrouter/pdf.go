package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// buildPDFBody assembles a one-shot chat request that attaches a PDF as a
// file-input content part and (optionally) enables the file-parser plugin with
// the chosen engine. An empty engine sends the PDF natively (no plugin) for
// native-file vision models like Gemini.
func buildPDFBody(model, path, engine, prompt string) (RequestBody, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RequestBody{}, fmt.Errorf("read pdf %q: %w", path, err)
	}
	dataURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)

	body := RequestBody{
		Model: model,
		Messages: []APIMessage{{
			Role: "user",
			Content: []ContentPart{
				{Type: "text", Text: prompt},
				{Type: "file", File: &FilePart{
					Filename: filepath.Base(path),
					FileData: dataURL,
				}},
			},
		}},
	}
	if engine != "" {
		body.Plugins = []Plugin{{ID: "file-parser", PDF: &PDFEngineSpec{Engine: engine}}}
	}
	return body, nil
}

// annotatedResponse mirrors the subset of the chat response carrying file-parser
// output: parsed text lives in message.annotations[].file.content[].
type annotatedResponse struct {
	Choices []struct {
		Message struct {
			Content     any `json:"content"`
			Annotations []struct {
				Type string `json:"type"`
				File struct {
					Name    string `json:"name"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"file"`
			} `json:"annotations"`
		} `json:"message"`
	} `json:"choices"`
}

// pagesFromAnnotations turns file-parser annotations into ordered text pages.
// Each text content item becomes one page; image items are skipped (OCR text is
// what we keep). Errors if the response carries no parsed file content.
func pagesFromAnnotations(raw []byte) ([]output.PDFPage, error) {
	var resp annotatedResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal annotated response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	var pages []output.PDFPage
	n := 0
	for _, ann := range resp.Choices[0].Message.Annotations {
		if ann.Type != "file" {
			continue
		}
		for _, c := range ann.File.Content {
			if c.Type != "text" || strings.TrimSpace(c.Text) == "" {
				continue
			}
			n++
			pages = append(pages, output.PDFPage{Number: n, Text: c.Text})
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("file-parser returned no text content")
	}
	return pages, nil
}

// pagesFromContent treats the model's free-text reply as a single-page reading.
// Used for vision extraction, where page structure is not guaranteed.
func pagesFromContent(raw []byte) ([]output.PDFPage, error) {
	var resp ResponseBody
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	text := extractContent(resp.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("vision model returned empty reading")
	}
	return []output.PDFPage{{Number: 1, Text: text}}, nil
}

// ExtractText parses a PDF with a server-side OCR engine and returns its pages.
func (c *Client) ExtractText(ctx context.Context, path, engine string) ([]output.PDFPage, error) {
	body, err := buildPDFBody(c.model, path, engine, "Извлеки весь текст из документа постранично.")
	if err != nil {
		return nil, err
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal pdf request: %w", err)
	}
	raw, err := c.doRequest(ctx, jsonBody)
	if err != nil {
		return nil, err
	}
	return pagesFromAnnotations(raw)
}

// ReadVision sends a PDF natively to a vision model and returns its reading.
func (c *Client) ReadVision(ctx context.Context, path, model, prompt string) ([]output.PDFPage, error) {
	if model == "" {
		model = c.model
	}
	body, err := buildPDFBody(model, path, "", prompt)
	if err != nil {
		return nil, err
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal vision request: %w", err)
	}
	raw, err := c.doRequest(ctx, jsonBody)
	if err != nil {
		return nil, err
	}
	return pagesFromContent(raw)
}

// Compile-time check that the client satisfies the extraction port.
var _ output.RemotePDFExtractor = (*Client)(nil)
