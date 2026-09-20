package extraction

import (
	"context"
	"errors"
	"testing"
)

const pdfinfoA4 = `Title:          Уведомление
Pages:          3
Page size:      595.276 x 841.89 pts (A4)
File size:      120334 bytes
`

const pdfinfoA1 = `Pages:          1
Page size:      2383.94 x 1683.78 pts (A1)
`

func fakePdfinfo(out string, err error) commandRunner {
	return func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(out), err
	}
}

func TestDenseScan_A4IsTextScan(t *testing.T) {
	detect := denseScanWith("pdfinfo", fakePdfinfo(pdfinfoA4, nil))
	if !detect("/tmp/notice.pdf") {
		t.Fatalf("A4 scan must go to OCR")
	}
}

func TestDenseScan_LargeSheetIsDrawing(t *testing.T) {
	detect := denseScanWith("pdfinfo", fakePdfinfo(pdfinfoA1, nil))
	if detect("/tmp/plan.pdf") {
		t.Fatalf("A1 sheet must go to vision")
	}
}

func TestDenseScan_PdfinfoFailureKeepsVision(t *testing.T) {
	detect := denseScanWith("pdfinfo", fakePdfinfo("", errors.New("no such file")))
	if detect("/tmp/x.pdf") {
		t.Fatalf("pdfinfo failure must default to vision")
	}
}

func TestDenseScan_NoPageSizeKeepsVision(t *testing.T) {
	detect := denseScanWith("pdfinfo", fakePdfinfo("Pages: 2\n", nil))
	if detect("/tmp/x.pdf") {
		t.Fatalf("missing page size must default to vision")
	}
}

func TestParsePageSize(t *testing.T) {
	w, h, ok := parsePageSize(pdfinfoA4)
	if !ok || w < 595 || w > 596 || h < 841 || h > 842 {
		t.Fatalf("parsePageSize = (%v, %v, %v)", w, h, ok)
	}
	if _, _, ok := parsePageSize("garbage"); ok {
		t.Fatalf("garbage must not parse")
	}
}
