package openrouter

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTempPDF(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp pdf: %v", err)
	}
	return path
}

func TestBuildPDFBody_FileInputAndPlugin(t *testing.T) {
	path := writeTempPDF(t, "%PDF-1.4 fake")
	body, err := buildPDFBody("mistralai/x", path, "mistral-ocr", "extract")
	if err != nil {
		t.Fatalf("buildPDFBody: %v", err)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msgs := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	parts := msgs[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("want 2 content parts (text+file), got %d", len(parts))
	}
	filePart := parts[1].(map[string]any)
	if filePart["type"] != "file" {
		t.Fatalf("want file part, got %v", filePart["type"])
	}
	fileObj := filePart["file"].(map[string]any)
	if fileObj["filename"] != "doc.pdf" {
		t.Fatalf("want filename doc.pdf, got %v", fileObj["filename"])
	}
	wantData := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake"))
	if fileObj["file_data"] != wantData {
		t.Fatalf("file_data mismatch")
	}

	plugins := got["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("want 1 plugin, got %d", len(plugins))
	}
	plugin := plugins[0].(map[string]any)
	if plugin["id"] != "file-parser" {
		t.Fatalf("want file-parser, got %v", plugin["id"])
	}
	if plugin["pdf"].(map[string]any)["engine"] != "mistral-ocr" {
		t.Fatalf("want mistral-ocr engine")
	}
}

func TestBuildPDFBody_NoEngineOmitsPlugin(t *testing.T) {
	path := writeTempPDF(t, "x")
	body, err := buildPDFBody("google/gemini-2.5-flash", path, "", "read")
	if err != nil {
		t.Fatalf("buildPDFBody: %v", err)
	}
	if body.Plugins != nil {
		t.Fatalf("want no plugins when engine empty, got %v", body.Plugins)
	}
}

func TestPagesFromAnnotations(t *testing.T) {
	respJSON := `{
		"choices": [{
			"message": {
				"content": "ack",
				"annotations": [{
					"type": "file",
					"file": {
						"name": "doc.pdf",
						"content": [
							{"type": "text", "text": "page one text"},
							{"type": "text", "text": "page two text"}
						]
					}
				}]
			}
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 1}
	}`

	pages, err := pagesFromAnnotations([]byte(respJSON))
	if err != nil {
		t.Fatalf("pagesFromAnnotations: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("want 2 pages, got %d", len(pages))
	}
	if pages[0].Number != 1 || pages[0].Text != "page one text" {
		t.Fatalf("page 1 mismatch: %+v", pages[0])
	}
	if pages[1].Number != 2 || pages[1].Text != "page two text" {
		t.Fatalf("page 2 mismatch: %+v", pages[1])
	}
}

func TestPagesFromContent_Vision(t *testing.T) {
	respJSON := `{
		"choices": [{"message": {"content": "Штамп: ООО Ромашка. Площадь 120 м2."}}],
		"usage": {"prompt_tokens": 500, "completion_tokens": 40}
	}`

	pages, err := pagesFromContent([]byte(respJSON))
	if err != nil {
		t.Fatalf("pagesFromContent: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("want 1 page, got %d", len(pages))
	}
	if pages[0].Number != 1 || pages[0].Text == "" {
		t.Fatalf("vision page mismatch: %+v", pages[0])
	}
}

func TestPagesFromAnnotations_NoFileAnnotation(t *testing.T) {
	respJSON := `{"choices":[{"message":{"content":"ack","annotations":[]}}],"usage":{}}`
	_, err := pagesFromAnnotations([]byte(respJSON))
	if err == nil {
		t.Fatalf("want error when no file annotation present")
	}
}

// mistral-ocr commonly returns the whole document as one markdown blob rather
// than per-page items; that must yield a single chunk, not a crash or 0 pages.
func TestPagesFromAnnotations_SingleBlob(t *testing.T) {
	respJSON := `{
		"choices": [{
			"message": {
				"content": "ack",
				"annotations": [{
					"type": "file",
					"file": {
						"name": "scan.pdf",
						"content": [{"type": "text", "text": "## Стр.1\nфул текст\n## Стр.2\nещё"}]
					}
				}]
			}
		}]
	}`
	pages, err := pagesFromAnnotations([]byte(respJSON))
	if err != nil {
		t.Fatalf("pagesFromAnnotations: %v", err)
	}
	if len(pages) != 1 || pages[0].Number != 1 {
		t.Fatalf("want one chunk numbered 1, got %+v", pages)
	}
}

func TestBuildPDFBody_RejectsOversizeFile(t *testing.T) {
	path := writeTempPDF(t, "small")
	if err := checkPDFSize(path, 1); err == nil {
		t.Fatalf("want error when file exceeds size limit")
	}
	if err := checkPDFSize(path, 1<<20); err != nil {
		t.Fatalf("unexpected error under limit: %v", err)
	}
}
