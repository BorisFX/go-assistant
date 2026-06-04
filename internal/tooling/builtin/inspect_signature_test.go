package builtin

import "testing"

func TestDecodeOpenSSLEscapes(t *testing.T) {
	got := decodeOpenSSLEscapes(`GN = \D0\9C\D0\B8\D1\85\D0\B0\D0\B8\D0\BB, SN = \D0\A7\D0\B0\D0\BF\D0\BB\D1\8B\D0\B3\D0\B8\D0\BD`)
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
	got := extractSignerSerials(in)
	if len(got) != 2 {
		t.Fatalf("want 2 signers, got %d: %v", len(got), got)
	}
	if got[0] != "C736DD4F924F1F7ABB20BBCF64072759" || got[1] != "031F4384008DB306B44B1BC70750C6D6FF" {
		t.Errorf("unexpected serials: %v", got)
	}
}

func TestExtractSignerSerialsDeduplicates(t *testing.T) {
	in := "issuer_and_serial:\n serial: 0xAB\nissuer_and_serial:\n serial: 0xAB"
	if got := extractSignerSerials(in); len(got) != 1 {
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
		if got := subjectAttr(subj, key); got != want {
			t.Errorf("attr %s: got %q want %q", key, got, want)
		}
	}
	if got := subjectAttr(subj, "title"); got != "" {
		t.Errorf("missing attr should be empty, got %q", got)
	}
}

func TestSubjectAttrKeepsInnerQuotes(t *testing.T) {
	subj := `OU = Удостоверяющий центр, O = ООО "ТАКСКОМ", CN = ООО "ТАКСКОМ"`
	if got := subjectAttr(subj, "O"); got != `ООО "ТАКСКОМ"` {
		t.Errorf("org: got %q", got)
	}
}

func TestParseSignerMissingCert(t *testing.T) {
	s := parseSigner("DEAD", "")
	if s.Serial != "DEAD" || s.Name == "" {
		t.Errorf("missing-cert signer should keep serial and flag name: %+v", s)
	}
}

func TestParseSignerFields(t *testing.T) {
	subj := `INN = 504001315854, SNILS = 02990247571, SN = Чаплыгин, CN = Чаплыгин Михаил Николаевич`
	s := parseSigner("031F", subj)
	if s.Name != "Чаплыгин Михаил Николаевич" || s.SNILS != "02990247571" || s.INN != "504001315854" {
		t.Errorf("parsed signer wrong: %+v", s)
	}
}

func TestSplitPEMCerts(t *testing.T) {
	pem := "noise\n-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nBBB\n-----END CERTIFICATE-----\n"
	blocks := splitPEMCerts(pem)
	if len(blocks) != 2 {
		t.Fatalf("want 2 cert blocks, got %d", len(blocks))
	}
}

func TestNormalizeSerial(t *testing.T) {
	if got := normalizeSerial(" 0xabc123 "); got != "ABC123" {
		t.Errorf("normalize: got %q", got)
	}
}
