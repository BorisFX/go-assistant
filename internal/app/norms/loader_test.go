package norms

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCorpus_ManifestAndDefaults(t *testing.T) {
	dir := writeCorpus(t, map[string]string{
		"a.md":          "Статья 1. Один",
		"ГрК РФ.txt":    "Статья 51. Разрешение",
		"skip.json":     "{}",
		"manifest.yaml": "a.md:\n  code: 218-ФЗ\n  edition: 2024\n",
	})
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	srcs, err := LoadCorpus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 {
		t.Fatalf("want 2 sources, got %d", len(srcs))
	}
	byCode := map[string]Source{}
	for _, s := range srcs {
		byCode[s.Document.Code] = s
	}
	if byCode["218-ФЗ"].Document.Edition != "2024" || byCode["218-ФЗ"].Text != "Статья 1. Один" {
		t.Fatalf("manifest entry: %+v", byCode["218-ФЗ"])
	}
	if _, ok := byCode["ГрК РФ"]; !ok {
		t.Fatalf("file name should become the code: %v", byCode)
	}
	if byCode["ГрК РФ"].Document.FileHash == "" || len(byCode["ГрК РФ"].Document.FileHash) != 64 {
		t.Fatal("sha256 hash expected")
	}
}

func TestLoadCorpus_BadManifestIsAnError(t *testing.T) {
	dir := writeCorpus(t, map[string]string{"manifest.yaml": "::not yaml"})
	if _, err := LoadCorpus(context.Background(), dir); err == nil {
		t.Fatal("expected manifest parse error")
	}
}

func TestLoadCorpus_MissingDir(t *testing.T) {
	if _, err := LoadCorpus(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error")
	}
}
