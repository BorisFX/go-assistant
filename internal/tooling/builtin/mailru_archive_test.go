package builtin

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeMemberName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a/b.pdf", filepath.Join("a", "b.pdf")},
		{`a\b\c.xml`, filepath.Join("a", "b", "c.xml")},
		{"/leading/slash.pdf", filepath.Join("leading", "slash.pdf")},
		{"../../etc/passwd", filepath.Join("etc", "passwd")},
		{"./x.pdf", "x.pdf"},
		{"..", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeMemberName(c.in); got != c.want {
			t.Fatalf("sanitizeMemberName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// writeTestZip builds a zip at path with the given member name→content map.
func writeTestZip(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZip_HarvestsOnlyAllowedExts(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "obj.zip")
	writeTestZip(t, src, map[string]string{
		"plan.pdf":          "PDF DATA",
		"techplan.xml":      "<xml>tech</xml>",
		"preview.jpg":       "binary",
		"nested/extra.pdf":  "NESTED PDF",
		"signature.xml.sig": "sig",
	})

	dest := filepath.Join(dir, "out")
	got, err := extractArchive(context.Background(), src, dest, []string{".pdf", ".xml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 harvested files (2 pdf + 1 xml), got %d: %v", len(got), got)
	}
	// .jpg and .xml.sig must be skipped.
	for _, p := range got {
		ext := filepath.Ext(p)
		if ext != ".pdf" && ext != ".xml" {
			t.Fatalf("unexpected harvested ext %q in %q", ext, p)
		}
	}
	// Content of an extracted member is intact.
	xmlPath := filepath.Join(dest, "techplan.xml")
	b, err := os.ReadFile(xmlPath)
	if err != nil || string(b) != "<xml>tech</xml>" {
		t.Fatalf("xml content mismatch: %q err=%v", string(b), err)
	}
}

func TestWriteMember_NeutralizesZipSlip(t *testing.T) {
	dest := t.TempDir()
	// A traversal name must be neutralized: the file lands inside dest, never
	// in the parent directory.
	got, err := writeMember(dest, "../escape.pdf", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("writeMember: %v", err)
	}
	if !strings.HasPrefix(got, filepath.Clean(dest)+string(os.PathSeparator)) {
		t.Fatalf("member written outside dest: %q", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.pdf")); err == nil {
		t.Fatal("zip-slip wrote a file outside destination")
	}
}

func TestFindLocalDocs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.pdf"), "x")
	mustWrite(t, filepath.Join(dir, "b.xml"), "x")
	mustWrite(t, filepath.Join(dir, "c.jpg"), "x")
	mustWrite(t, filepath.Join(dir, "sub", "d.PDF"), "x")

	got := findLocalDocs(dir, []string{".pdf", ".xml"})
	if len(got) != 3 {
		t.Fatalf("want 3 docs, got %d: %v", len(got), got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
