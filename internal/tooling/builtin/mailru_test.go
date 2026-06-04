package builtin

import (
	"reflect"
	"testing"
)

func TestParseDavChildren(t *testing.T) {
	body := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
	<d:response>
		<d:href>/%D0%A0%D0%90%D0%91%D0%9E%D0%A2%D0%90/</d:href>
		<d:propstat><d:prop><d:displayname>РАБОТА</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat>
	</d:response>
	<d:response>
		<d:href>/x/folder</d:href>
		<d:propstat><d:prop><d:displayname>РУСКОН Раменское</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat>
	</d:response>
	<d:response>
		<d:href>/x/file.pdf</d:href>
		<d:propstat><d:prop><d:displayname>Технический план.pdf</d:displayname><d:resourcetype/></d:prop></d:propstat>
	</d:response>
</d:multistatus>`

	got := parseDavChildren(body)
	want := []davChild{
		{name: "РУСКОН Раменское", isDir: true},
		{name: "Технический план.pdf", isDir: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDavChildren = %+v, want %+v", got, want)
	}
}

func TestTokenize(t *testing.T) {
	got := tokenize("ПРОИЗВОДСТВО РУСКОН / НА РВ / Техплан старый +приостановка")
	want := []string{"производство", "рускон", "на", "рв", "техплан", "старый", "приостановка"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokenize = %v, want %v", got, want)
	}
	// single-character noise is dropped
	if g := tokenize("a б 12 -"); !reflect.DeepEqual(g, []string{"12"}) {
		t.Errorf("tokenize noise = %v, want [12]", g)
	}
}

func TestPathLeaf(t *testing.T) {
	cases := map[string]string{
		"/РУСКОН Раменское/на РВ/file.pdf": "file.pdf",
		"/РУСКОН Раменское/на РВ/":         "на РВ",
		"/top/":                            "top",
		"file.pdf":                         "file.pdf",
	}
	for in, want := range cases {
		if got := pathLeaf(in); got != want {
			t.Errorf("pathLeaf(%q) = %q, want %q", in, got, want)
		}
	}
}

// The query carries extra words the user misremembered ("производство",
// "приостановка"); the deeply-nested real file must still rank first.
func TestSearchIndexRanksNestedFileFirst(t *testing.T) {
	entries := []indexEntry{
		{Path: "/РУСКОН Раменское/", IsDir: true},
		{Path: "/РУСКОН Раменское/на РВ/", IsDir: true},
		{Path: "/РУСКОН Раменское/на РВ/Техплан старый.pdf", IsDir: false},
		{Path: "/Производство Ногинск/смета.xls", IsDir: false},
		{Path: "/прочее/договор.pdf", IsDir: false},
	}
	tokens := tokenize("ПРОИЗВОДСТВО РУСКОН НА РВ Техплан старый приостановка")

	matches := searchIndex(entries, tokens)
	if len(matches) == 0 {
		t.Fatal("expected matches, got none")
	}
	if matches[0].Path != "/РУСКОН Раменское/на РВ/Техплан старый.pdf" {
		t.Errorf("top match = %q, want the nested techplan file", matches[0].Path)
	}
	// The unrelated договор.pdf shares no two tokens and must be excluded.
	for _, m := range matches {
		if m.Path == "/прочее/договор.pdf" {
			t.Errorf("unrelated file should not match: %q", m.Path)
		}
	}
}

func TestSearchIndexSingleTokenNeedsOneHit(t *testing.T) {
	entries := []indexEntry{
		{Path: "/РУСКОН Раменское/", IsDir: true},
		{Path: "/прочее/договор.pdf", IsDir: false},
	}
	matches := searchIndex(entries, tokenize("рускон"))
	if len(matches) != 1 || matches[0].Path != "/РУСКОН Раменское/" {
		t.Errorf("single-token search = %+v, want only РУСКОН folder", matches)
	}
}
