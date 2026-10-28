package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tablePDF builds a two-page PDF whose first page holds a three-column table
// drawn column by column — exactly how CAD exports lay out an экспликация.
// Without -layout the extracted text degenerates into three unrelated lists,
// and a row can only be reassembled by guessing.
func tablePDF(t *testing.T) string {
	t.Helper()

	page1 := `BT /F1 10 Tf 100 700 Td (1) Tj ET
BT /F1 10 Tf 100 680 Td (2) Tj ET
BT /F1 10 Tf 250 700 Td (Vhodnoy tambur) Tj ET
BT /F1 10 Tf 250 680 Td (Torgovyi zal) Tj ET
BT /F1 10 Tf 450 700 Td (18,45) Tj ET
BT /F1 10 Tf 450 680 Td (422,18) Tj ET
`
	page2 := "BT /F1 10 Tf 100 700 Td (SPOZU sheet marker) Tj ET\n"

	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Resources<</Font<</F1 5 0 R>>>>/Contents 6 0 R>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Resources<</Font<</F1 5 0 R>>>>/Contents 7 0 R>>",
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(page1), page1),
		fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(page2), page2),
	}

	var buf strings.Builder
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)

	path := filepath.Join(t.TempDir(), "table.pdf")
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func readPDFText(t *testing.T, params string) map[string]any {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	raw, err := NewReadPDF().Execute(context.Background(), json.RawMessage(params))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// A table row must survive extraction as one line: the room number, its name
// and its area belong together. Mis-zipping those columns is what made the bot
// report the wrong торговая площадь.
func TestReadPDFKeepsTableRowsIntact(t *testing.T) {
	path := tablePDF(t)
	out := readPDFText(t, fmt.Sprintf(`{"path":%q}`, path))

	text, _ := out["text"].(string)
	var row string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Torgovyi zal") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("row not found in extracted text:\n%s", text)
	}
	if !strings.Contains(row, "422,18") || !strings.Contains(row, "2") {
		t.Errorf("table row lost its columns: %q\nfull text:\n%s", row, text)
	}
}

// Drawing sets run to dozens of sheets; reading one sheet must not mean
// pulling the whole album through the context window.
func TestReadPDFPageRange(t *testing.T) {
	path := tablePDF(t)
	out := readPDFText(t, fmt.Sprintf(`{"path":%q,"first_page":2,"last_page":2}`, path))

	text, _ := out["text"].(string)
	if !strings.Contains(text, "SPOZU sheet marker") {
		t.Errorf("page 2 not returned: %q", text)
	}
	if strings.Contains(text, "Torgovyi zal") {
		t.Errorf("page 1 leaked into a page-2 read: %q", text)
	}
	if pages, _ := out["pages"].(string); pages != "2-2" {
		t.Errorf("result should state the page range it read, got %q", pages)
	}
}
