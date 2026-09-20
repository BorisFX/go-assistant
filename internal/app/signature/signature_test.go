package signature

import (
	"context"
	"strings"
	"testing"
)

func TestDecodeOpenSSLEscapes(t *testing.T) {
	got := DecodeOpenSSLEscapes(`GN = \D0\9C\D0\B8\D1\85\D0\B0\D0\B8\D0\BB, SN = \D0\A7\D0\B0\D0\BF\D0\BB\D1\8B\D0\B3\D0\B8\D0\BD`)
	want := "GN = Михаил, SN = Чаплыгин"
	if got != want {
		t.Errorf("decode mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestExtractSignerSerials(t *testing.T) {
	in := `
  signerInfo:
        issuer_and_serial:
          issuer: CN=Federal Treasury
          serial: 0xC736DD4F924F1F7ABB20BBCF64072759
        digest_alg:
  signerInfo:
        issuer_and_serial:
          issuer: CN=TAXCOM
          serial: 0x031F4384008DB306B44B1BC70750C6D6FF
        digest_alg:
`
	got := ExtractSignerSerials(in)
	if len(got) != 2 {
		t.Fatalf("want 2 signers, got %d: %v", len(got), got)
	}
	if got[0] != "C736DD4F924F1F7ABB20BBCF64072759" || got[1] != "031F4384008DB306B44B1BC70750C6D6FF" {
		t.Errorf("unexpected serials: %v", got)
	}
}

func TestExtractSignerSerialsDeduplicates(t *testing.T) {
	in := "issuer_and_serial:\n serial: 0xAB\nissuer_and_serial:\n serial: 0xAB"
	if got := ExtractSignerSerials(in); len(got) != 1 {
		t.Errorf("want 1 deduplicated serial, got %v", got)
	}
}

func TestSubjectAttr(t *testing.T) {
	subj := `INN = 504001315854, SNILS = 02990247571, GN = Михаил Николаевич, SN = Чаплыгин, CN = Чаплыгин Михаил Николаевич`
	cases := map[string]string{
		"CN":    "Чаплыгин Михаил Николаевич",
		"INN":   "504001315854",
		"SNILS": "02990247571",
	}
	for key, want := range cases {
		if got := SubjectAttr(subj, key); got != want {
			t.Errorf("attr %s: got %q want %q", key, got, want)
		}
	}
	if got := SubjectAttr(subj, "title"); got != "" {
		t.Errorf("missing attr should be empty, got %q", got)
	}
}

func TestParseSignerFields(t *testing.T) {
	subj := `INN = 504001315854, SNILS = 02990247571, SN = Чаплыгин, CN = Чаплыгин Михаил Николаевич`
	s := ParseSigner("031F", subj)
	if s.Name != "Чаплыгин Михаил Николаевич" || s.SNILS != "02990247571" || s.INN != "504001315854" {
		t.Errorf("parsed signer wrong: %+v", s)
	}
	if m := ParseSigner("DEAD", ""); m.Serial != "DEAD" || m.Name == "" {
		t.Errorf("missing-cert signer should keep serial and flag name: %+v", m)
	}
}

func TestSplitPEMCerts(t *testing.T) {
	pem := "noise\n-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nBBB\n-----END CERTIFICATE-----\n"
	if blocks := SplitPEMCerts(pem); len(blocks) != 2 {
		t.Fatalf("want 2 cert blocks, got %d", len(blocks))
	}
}

func TestNormalizeSerial(t *testing.T) {
	if got := NormalizeSerial(" 0xabc123 "); got != "ABC123" {
		t.Errorf("normalize: got %q", got)
	}
}

func TestInspectRejectsMissingFile(t *testing.T) {
	if _, err := Inspect(context.Background(), "/nonexistent/x.sig"); err == nil {
		t.Fatalf("want error for missing file")
	}
	if _, err := Inspect(context.Background(), ""); err == nil {
		t.Fatalf("want error for empty path")
	}
}

func TestRenderCarriesSignersAndCaveat(t *testing.T) {
	info := Info{
		Path:           "/d/техплан.xml.sig",
		SignatureCount: 2,
		Signers: []Signer{
			{Name: "Чаплыгин Михаил Николаевич", Org: `ООО "ГЕО"`, Title: "кадастровый инженер", INN: "504001315854", SNILS: "02990247571", Serial: "031F"},
			{Name: "(certificate not present in container)", Serial: "DEAD"},
		},
	}
	out := Render(info)
	for _, want := range []string{"Подписей: 2", "Подписант 1: Чаплыгин", `ООО "ГЕО"`, "кадастровый инженер", "ИНН: 504001315854", "СНИЛС: 02990247571", "031F", "Подписант 2", "НЕ выполнялась"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderWarnsOnNoSigners(t *testing.T) {
	out := Render(Info{Path: "/d/x.sig", Warning: "No signerInfo found"})
	if !strings.Contains(out, "Подписей: 0") || !strings.Contains(out, "Предупреждение") {
		t.Errorf("render must surface the warning: %s", out)
	}
}
