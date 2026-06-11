package extraction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type fakeLocal struct {
	pages []output.PDFPage
	err   error
}

func (f *fakeLocal) Extract(_ context.Context, _ string) ([]output.PDFPage, error) {
	return f.pages, f.err
}

type fakeRemote struct {
	textPages   []output.PDFPage
	visionPages []output.PDFPage
	textErr     error
	visionErr   error
	textCalls   int
	visionCalls int
	lastEngine  string
}

func (f *fakeRemote) ExtractText(_ context.Context, _, engine string) ([]output.PDFPage, error) {
	f.textCalls++
	f.lastEngine = engine
	return f.textPages, f.textErr
}

func (f *fakeRemote) ReadVision(_ context.Context, _, _, _ string) ([]output.PDFPage, error) {
	f.visionCalls++
	return f.visionPages, f.visionErr
}

func TestRouter_TextPDFUsesLocal(t *testing.T) {
	local := &fakeLocal{pages: []output.PDFPage{{Number: 1, Text: "contract"}}}
	remote := &fakeRemote{}
	r := NewRouter(remote, WithLocal(local), WithVisionModel("google/gemini-2.5-flash"))

	res, err := r.Extract(context.Background(), "/tmp/contract.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Method != "pdftotext" || len(res.Pages) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if remote.textCalls != 0 || remote.visionCalls != 0 {
		t.Fatalf("remote must not be called for text PDFs")
	}
}

func TestRouter_EmptyDefaultsToVision(t *testing.T) {
	local := &fakeLocal{pages: nil}
	remote := &fakeRemote{visionPages: []output.PDFPage{{Number: 1, Text: "Штамп: ООО"}}}
	r := NewRouter(remote, WithLocal(local), WithVisionModel("google/gemini-2.5-flash"))

	res, err := r.Extract(context.Background(), "/tmp/plan.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Method != "vision" || remote.visionCalls != 1 {
		t.Fatalf("want vision path, got %+v calls=%d", res, remote.visionCalls)
	}
	if remote.textCalls != 0 {
		t.Fatalf("OCR must not run by default")
	}
}

func TestRouter_DenseScanEscalatesToOCR(t *testing.T) {
	local := &fakeLocal{pages: nil}
	remote := &fakeRemote{textPages: []output.PDFPage{{Number: 1, Text: "scanned contract"}}}
	r := NewRouter(remote,
		WithLocal(local),
		WithVisionModel("google/gemini-2.5-flash"),
		WithDenseScanDetector(func(string) bool { return true }),
	)

	res, err := r.Extract(context.Background(), "/tmp/scan.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Method != "mistral-ocr" || remote.lastEngine != "mistral-ocr" {
		t.Fatalf("want mistral-ocr path, got %+v engine=%s", res, remote.lastEngine)
	}
	if remote.visionCalls != 0 {
		t.Fatalf("vision must not run when dense-scan detected")
	}
}

func TestRouter_XMLReadDirectly(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "techplan.xml")
	if err := os.WriteFile(xmlPath, []byte("<tech><area>120</area></tech>"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := &fakeLocal{err: errors.New("pdftotext must not run on xml")}
	remote := &fakeRemote{}
	r := NewRouter(remote, WithLocal(local), WithVisionModel("m"))

	res, err := r.Extract(context.Background(), xmlPath)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Method != "text" || len(res.Pages) != 1 || res.Pages[0].Text != "<tech><area>120</area></tech>" {
		t.Fatalf("unexpected xml result: %+v", res)
	}
	if remote.textCalls != 0 || remote.visionCalls != 0 {
		t.Fatalf("remote must not be called for xml")
	}
}

func TestRouter_LocalErrorPropagates(t *testing.T) {
	local := &fakeLocal{err: errors.New("boom")}
	r := NewRouter(&fakeRemote{}, WithLocal(local))
	if _, err := r.Extract(context.Background(), "/tmp/x.pdf"); err == nil {
		t.Fatalf("want error from local extractor")
	}
}

func TestRouter_VisionErrorPropagates(t *testing.T) {
	local := &fakeLocal{pages: nil}
	remote := &fakeRemote{visionErr: errors.New("vision down")}
	r := NewRouter(remote, WithLocal(local), WithVisionModel("m"))
	if _, err := r.Extract(context.Background(), "/tmp/x.pdf"); err == nil {
		t.Fatalf("want error from vision extractor")
	}
}
