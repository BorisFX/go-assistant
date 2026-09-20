package extraction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeScript stands in for dwg_extract.py: a shell script is enough to prove
// the wiring — the real reader is exercised against a real drawing on the box.
func fakeScript(t *testing.T, body string) (shell, script string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "reader.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return "/bin/sh", script
}

func TestIsCAD(t *testing.T) {
	cases := map[string]bool{
		"АР 18.03.26.dwg": true,
		"план.DXF":        true,
		"Уведомление.pdf": false,
		"GKUOKS_0004.zip": false,
		"техплан.xml":     false,
		"чертёж.dwg.bak":  false,
	}
	for name, want := range cases {
		if got := IsCAD(name); got != want {
			t.Errorf("IsCAD(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDWGExtractorRequiresRealPaths(t *testing.T) {
	if _, err := NewDWGExtractor("", ""); err == nil {
		t.Error("пустые пути должны отвергаться")
	}
	if _, err := NewDWGExtractor("/bin/sh", "/нет/такого/скрипта.py"); err == nil {
		t.Error("отсутствующий скрипт должен отвергаться на старте, а не при первом чертеже")
	}
}

func TestDWGExtractorReturnsDrawingText(t *testing.T) {
	shell, script := fakeScript(t, `echo "ЧЕРТЁЖ: $(basename "$1")"; echo "Площадь застройки 2429,3"`)
	reader, err := NewDWGExtractor(shell, script)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	pages, err := reader.Extract(context.Background(), "/tmp/АР 18.03.26.dwg")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(pages) != 1 || pages[0].Number != 1 {
		t.Fatalf("страницы: %+v", pages)
	}
	if !strings.Contains(pages[0].Text, "2429,3") {
		t.Errorf("текст чертежа: %q", pages[0].Text)
	}
}

// The reason a drawing could not be read has to reach the user: a silent empty
// result would be read as "на чертеже ничего нет".
func TestDWGExtractorSurfacesScriptError(t *testing.T) {
	shell, script := fakeScript(t, `echo "dwg2dxf не найден" >&2; exit 4`)
	reader, _ := NewDWGExtractor(shell, script)

	_, err := reader.Extract(context.Background(), "/tmp/план.dwg")
	if err == nil || !strings.Contains(err.Error(), "dwg2dxf не найден") {
		t.Fatalf("ожидалась причина от скрипта, получено: %v", err)
	}
}

func TestDWGExtractorRejectsEmptyOutput(t *testing.T) {
	shell, script := fakeScript(t, `exit 0`)
	reader, _ := NewDWGExtractor(shell, script)

	if _, err := reader.Extract(context.Background(), "/tmp/пустой.dxf"); err == nil {
		t.Error("пустой вывод не должен выдаваться за прочитанный чертёж")
	}
}

// Routing: with a CAD reader configured, a drawing must never reach the vision
// model — that is the whole point of reading it structurally.
func TestRouterSendsDrawingsToCADReader(t *testing.T) {
	shell, script := fakeScript(t, `echo "СЛОИ: оси, размер, штамп"`)
	reader, err := NewDWGExtractor(shell, script)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	remote := &fakeRemote{}
	router := NewRouter(remote, WithCADExtractor(reader))

	res, err := router.Extract(context.Background(), filepath.Join(t.TempDir(), "план.dwg"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Method != "dwg" {
		t.Errorf("метод: %q", res.Method)
	}
	if remote.visionCalls != 0 {
		t.Errorf("чертёж ушёл в vision %d раз", remote.visionCalls)
	}
	if !strings.Contains(res.Pages[0].Text, "штамп") {
		t.Errorf("текст: %q", res.Pages[0].Text)
	}
}

func TestIsOffice(t *testing.T) {
	cases := map[string]bool{
		"Договор подряда.docx": true,
		"Смета КС-3.xlsx":      true,
		"Акт.doc":              true,
		"старая.xls":           true,
		"пояснение.rtf":        true,
		"Уведомление.pdf":      false,
		"АР.dwg":               false,
	}
	for name, want := range cases {
		if got := IsOffice(name); got != want {
			t.Errorf("IsOffice(%q) = %v, want %v", name, got, want)
		}
	}
}

// An estimate must come back with its columns: the right-hand ones (цена,
// сумма) are exactly what the review is about.
func TestOfficeExtractorReturnsSheetText(t *testing.T) {
	shell, script := fakeScript(t, `echo "=== ЛИСТ: Смета ==="; printf 'Позиция\tЦена\tСумма\n'`)
	reader, err := NewOfficeExtractor(shell, script)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	pages, err := reader.Extract(context.Background(), "/tmp/Смета.xlsx")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(pages[0].Text, "Сумма") {
		t.Errorf("текст: %q", pages[0].Text)
	}
}

func TestOfficeExtractorSurfacesReason(t *testing.T) {
	shell, script := fakeScript(t, `echo "LibreOffice не смог открыть файл" >&2; exit 3`)
	reader, _ := NewOfficeExtractor(shell, script)

	_, err := reader.Extract(context.Background(), "/tmp/битый.doc")
	if err == nil || !strings.Contains(err.Error(), "LibreOffice") {
		t.Fatalf("ожидалась причина: %v", err)
	}
}

// Routing: a contract must reach the office reader, not pdftotext, which fails
// on every office format and leaves the document silently unread.
func TestRouterSendsOfficeDocumentsToOfficeReader(t *testing.T) {
	shell, script := fakeScript(t, `echo "ДОГОВОР ПОДРЯДА №17"`)
	reader, err := NewOfficeExtractor(shell, script)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	local := &fakeLocal{}
	router := NewRouter(&fakeRemote{}, WithLocal(local), WithOfficeExtractor(reader))

	res, err := router.Extract(context.Background(), "/tmp/Договор.docx")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Method != "office" {
		t.Errorf("метод: %q", res.Method)
	}
	if local.calls != 0 {
		t.Errorf("документ ушёл в pdftotext %d раз", local.calls)
	}
}
