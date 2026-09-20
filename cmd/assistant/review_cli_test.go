package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectLocalDirFiltersAndRecurses(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "том1")
	os.MkdirAll(sub, 0o755)
	for _, name := range []string{"а.pdf", "б.txt", "том1/в.DOCX", "том1/г.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := collectLocalDir(dir, []string{".pdf", ".docx"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("собрано %d файлов, ждали 2: %v", len(paths), paths)
	}
}

func TestCollectLocalDirGuardsCount(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"а.pdf", "б.pdf"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	if _, err := collectLocalDir(dir, []string{".pdf"}, 1); err == nil {
		t.Error("лимит файлов не сработал")
	}
}

func TestCollectLocalDirEmptyIsError(t *testing.T) {
	if _, err := collectLocalDir(t.TempDir(), []string{".pdf"}, 0); err == nil {
		t.Error("пустая папка должна быть ошибкой")
	}
}
