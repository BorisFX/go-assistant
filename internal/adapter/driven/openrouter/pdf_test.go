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
