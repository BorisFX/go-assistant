package builtin

import "testing"

func TestNormalizeSub(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"/", ""},
		{"Объект1", "/Объект1"},
		{"/Объект1", "/Объект1"},
		{"Проекты/Дом 5/", "/Проекты/Дом 5"},
		{"  /a/b  ", "/a/b"},
	}
	for _, c := range cases {
		if got := normalizeSub(c.in); got != c.want {
			t.Fatalf("normalizeSub(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtAllowed(t *testing.T) {
	exts := []string{".pdf", ".docx", ".xls"}
	cases := []struct {
		path string
		want bool
	}{
		{"/a/file.PDF", true},
		{"/a/file.docx", true},
		{"/a/file.txt", false},
		{"/a/dir/", false},
	}
	for _, c := range cases {
		if got := extAllowed(c.path, exts); got != c.want {
			t.Fatalf("extAllowed(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	// Empty exts → everything allowed.
	if !extAllowed("/a/x.anything", nil) {
		t.Fatalf("extAllowed with empty exts must allow any file")
	}
}

func TestFilterIndexByPrefixExt(t *testing.T) {
	entries := []indexEntry{
		{Path: "/Объект1/", IsDir: true},
		{Path: "/Объект1/dogovor.pdf", IsDir: false},
		{Path: "/Объект1/sub/", IsDir: true},
		{Path: "/Объект1/sub/plan.PDF", IsDir: false},
		{Path: "/Объект1/notes.txt", IsDir: false},
		{Path: "/Other/skip.pdf", IsDir: false},
	}
	got := filterIndexByPrefixExt(entries, "/Объект1", []string{".pdf"})
	want := []string{"/Объект1/dogovor.pdf", "/Объект1/sub/plan.PDF"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d want %s got %s", i, want[i], got[i])
		}
	}

	// Empty prefix → all files (recursive from root), dirs excluded.
	all := filterIndexByPrefixExt(entries, "", []string{".pdf"})
	if len(all) != 3 {
		t.Fatalf("empty prefix want 3 pdf files, got %v", all)
	}
}
