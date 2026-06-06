package legalreview

import "testing"

func TestParseReviewFolder(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"разбери папку Объект1", "Объект1", true},
		{"  Разбери Папку  /Проекты/Дом 5  ", "/Проекты/Дом 5", true},
		{"разбери папку \"Объект 2\"", "Объект 2", true},
		{"привет", "", false},
		{"разбери папку", "", false},
	}
	for _, c := range cases {
		got, ok := ParseReviewFolder(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("ParseReviewFolder(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFilterByExt(t *testing.T) {
	in := []string{"/a.PDF", "/b.txt", "/c.docx", "/d.jpg", "/e.xls"}
	got := filterByExt(in, []string{".pdf", ".doc", ".docx", ".xls", ".xlsx"})
	want := []string{"/a.PDF", "/c.docx", "/e.xls"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d want %s got %s", i, want[i], got[i])
		}
	}
}
