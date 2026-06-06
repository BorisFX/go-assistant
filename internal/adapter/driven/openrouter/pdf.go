package openrouter

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
