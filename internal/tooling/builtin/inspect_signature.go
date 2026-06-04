package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// InspectSignature parses a detached CMS/PKCS7 signature (.sig, DER) and reports
// the real signers (from leaf certificates) and the number of signatures. It lets
// the model verify "who signed" from facts instead of guessing by file name.
type InspectSignature struct{}

func NewInspectSignature() *InspectSignature { return &InspectSignature{} }

func (s *InspectSignature) Name() string { return "inspect_signature" }

func (s *InspectSignature) Description() string {
	return "Inspect a detached electronic signature file (.sig, CMS/PKCS7, DER) on the server: returns the real signers (names/orgs from leaf certificates) and how many signatures it holds. Use this instead of guessing signers from the file name. Russian GOST signatures are supported (certificate subjects are read; the crypto is not verified)."
}

func (s *InspectSignature) Category() string { return "documents" }

func (s *InspectSignature) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Local server path to a .sig file (detached CMS/PKCS7 signature, DER). Download or unzip it first if it is not already on disk."
			}
		},
		"required": ["path"]
	}`)
}

type inspectSigParams struct {
	Path string `json:"path"`
}

type sigSigner struct {
	Name   string `json:"name,omitempty"`
	Org    string `json:"organization,omitempty"`
	Title  string `json:"title,omitempty"`
	SNILS  string `json:"snils,omitempty"`
	INN    string `json:"inn,omitempty"`
	OGRNIP string `json:"ogrnip,omitempty"`
	Serial string `json:"serial"`
}

func (s *InspectSignature) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p inspectSigParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if _, err := os.Stat(p.Path); err != nil {
		return nil, fmt.Errorf("file not found: %s", p.Path)
	}

	printOut, err := opensslPKCS7Print(ctx, p.Path)
	if err != nil {
		return nil, fmt.Errorf("openssl could not read the file (not a DER CMS/PKCS7 .sig?): %w", err)
	}
	signerSerials := extractSignerSerials(printOut)

	certs, err := extractCertSubjects(ctx, p.Path)
	if err != nil {
		return nil, err
	}

	signers := make([]sigSigner, 0, len(signerSerials))
	for _, ser := range signerSerials {
		signers = append(signers, parseSigner(ser, certs[ser]))
	}

	result := map[string]any{
		"path":            p.Path,
		"signature_count": len(signerSerials),
		"signers":         signers,
		"instruction":     "signature_count is the number of real signatures. 'signers' are the actual signing persons/organizations (leaf certificates), NOT certificate-authority chain certs. State signers only from this data; never infer them from the file name. If a document carries a second signature from a government body, that is the issuer's own signature and is normal.",
	}
	if len(signerSerials) == 0 {
		result["warning"] = "No signerInfo found — the file may not be a detached CMS signature, or openssl could not parse it."
	}
	return json.Marshal(result)
}

func opensslPKCS7Print(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "openssl", "pkcs7", "-inform", "DER", "-in", path, "-print")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

var reSerialLine = regexp.MustCompile(`(?i)serial:\s*0x([0-9A-Fa-f]+)`)

// extractSignerSerials returns the serial of each signerInfo — i.e. the actual
// signatures, excluding the CA-chain certificates that also ride in the container.
func extractSignerSerials(printOut string) []string {
	var serials []string
	seen := map[string]bool{}
	lines := strings.Split(printOut, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "issuer_and_serial") {
			continue
		}
		for j := i + 1; j < len(lines) && j < i+8; j++ {
			if m := reSerialLine.FindStringSubmatch(lines[j]); m != nil {
				ser := normalizeSerial(m[1])
				if !seen[ser] {
					seen[ser] = true
					serials = append(serials, ser)
				}
				break
			}
		}
	}
	return serials
}

// extractCertSubjects maps each embedded certificate's serial to its decoded subject.
func extractCertSubjects(ctx context.Context, path string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "openssl", "pkcs7", "-inform", "DER", "-in", path, "-print_certs")
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run() // GOST may emit warnings to stderr; PEM still lands on stdout

	certs := map[string]string{}
	for _, block := range splitPEMCerts(out.String()) {
		ser, subj := certSerialSubject(ctx, block)
		if ser != "" {
			certs[ser] = subj
		}
	}
	return certs, nil
}

func certSerialSubject(ctx context.Context, pem string) (serial, subject string) {
	cmd := exec.CommandContext(ctx, "openssl", "x509", "-noout", "-serial", "-subject")
	cmd.Stdin = strings.NewReader(pem)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", ""
	}
	for line := range strings.SplitSeq(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "serial="):
			serial = normalizeSerial(strings.TrimPrefix(line, "serial="))
		case strings.HasPrefix(line, "subject="):
			subject = decodeOpenSSLEscapes(strings.TrimPrefix(line, "subject="))
		}
	}
	return serial, subject
}

func splitPEMCerts(pem string) []string {
	const begin = "-----BEGIN CERTIFICATE-----"
	var out []string
	for part := range strings.SplitSeq(pem, begin) {
		part = strings.TrimSpace(part)
		// Skip the preamble before the first marker and any non-cert fragment;
		// real certificate bodies always carry the END marker.
		if !strings.Contains(part, "END CERTIFICATE") {
			continue
		}
		out = append(out, begin+"\n"+part)
	}
	return out
}

func normalizeSerial(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	return strings.ToUpper(s)
}

var reHexEsc = regexp.MustCompile(`\\([0-9A-Fa-f]{2})`)

// decodeOpenSSLEscapes turns openssl's \XX escapes for non-ASCII bytes back into
// readable UTF-8 (Cyrillic subjects are emitted byte-by-byte as \D0\9C ...).
func decodeOpenSSLEscapes(s string) string {
	return reHexEsc.ReplaceAllStringFunc(s, func(m string) string {
		b, err := strconv.ParseUint(m[1:], 16, 8)
		if err != nil {
			return m
		}
		return string([]byte{byte(b)})
	})
}

func parseSigner(serial, subject string) sigSigner {
	s := sigSigner{Serial: serial}
	if subject == "" {
		s.Name = "(certificate not present in container)"
		return s
	}
	s.Name = subjectAttr(subject, "CN")
	s.Org = subjectAttr(subject, "O")
	s.Title = subjectAttr(subject, "title")
	s.SNILS = subjectAttr(subject, "SNILS")
	s.INN = subjectAttr(subject, "INN")
	if v := subjectAttr(subject, "1.2.643.100.5"); v != "" { // OGRNIP OID
		s.OGRNIP = v
	}
	return s
}

// subjectAttr extracts the value of a single RDN (e.g. CN, O, SNILS) from an
// openssl subject string. The fields we read never contain commas, so a simple
// comma-terminated capture is sufficient.
func subjectAttr(subject, key string) string {
	re := regexp.MustCompile(`(?:^|, )` + regexp.QuoteMeta(key) + ` = ([^,]+)`)
	m := re.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}
