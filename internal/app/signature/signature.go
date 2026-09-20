// Package signature reads detached CMS/PKCS7 signature files (.sig, DER) and
// reports who signed: the leaf certificates' subjects, not the CA chain. It
// deliberately does NOT verify the cryptography — Russian GOST signatures need
// a GOST engine that the server does not carry — so every caller must say so.
package signature

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Signer is one signing person or organization read from a leaf certificate.
type Signer struct {
	Name   string `json:"name,omitempty"`
	Org    string `json:"organization,omitempty"`
	Title  string `json:"title,omitempty"`
	SNILS  string `json:"snils,omitempty"`
	INN    string `json:"inn,omitempty"`
	OGRNIP string `json:"ogrnip,omitempty"`
	Serial string `json:"serial"`
}

// Info is what a .sig file tells about itself.
type Info struct {
	Path           string   `json:"path"`
	SignatureCount int      `json:"signature_count"`
	Signers        []Signer `json:"signers"`
	Warning        string   `json:"warning,omitempty"`
}

// CryptoNotice is the caveat every consumer must carry alongside the signers.
const CryptoNotice = "прочитаны данные сертификата; криптографическая проверка подписи и срока действия сертификата НЕ выполнялась"

// Inspect parses a detached signature and returns its real signers.
func Inspect(ctx context.Context, path string) (Info, error) {
	if path == "" {
		return Info{}, fmt.Errorf("path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return Info{}, fmt.Errorf("file not found: %s", path)
	}

	printOut, err := opensslPKCS7Print(ctx, path)
	if err != nil {
		return Info{}, fmt.Errorf("openssl could not read the file (not a DER CMS/PKCS7 .sig?): %w", err)
	}
	serials := ExtractSignerSerials(printOut)

	certs, err := extractCertSubjects(ctx, path)
	if err != nil {
		return Info{}, err
	}

	info := Info{Path: path, SignatureCount: len(serials), Signers: make([]Signer, 0, len(serials))}
	for _, ser := range serials {
		info.Signers = append(info.Signers, ParseSigner(ser, certs[ser]))
	}
	if len(serials) == 0 {
		info.Warning = "No signerInfo found — the file may not be a detached CMS signature, or openssl could not parse it."
	}
	return info, nil
}

// Render turns the parsed signature into one page of Russian text for the
// review pipeline, caveat included, so the coordinator never mistakes "we read
// the certificate" for "the signature is valid".
func Render(info Info) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Файл подписи: %s. Подписей: %d.\n", info.Path, info.SignatureCount)
	for i, s := range info.Signers {
		fmt.Fprintf(&b, "Подписант %d: %s", i+1, orDash(s.Name))
		if s.Org != "" {
			fmt.Fprintf(&b, ", организация: %s", s.Org)
		}
		if s.Title != "" {
			fmt.Fprintf(&b, ", должность: %s", s.Title)
		}
		if s.INN != "" {
			fmt.Fprintf(&b, ", ИНН: %s", s.INN)
		}
		if s.SNILS != "" {
			fmt.Fprintf(&b, ", СНИЛС: %s", s.SNILS)
		}
		if s.OGRNIP != "" {
			fmt.Fprintf(&b, ", ОГРНИП: %s", s.OGRNIP)
		}
		fmt.Fprintf(&b, ", серийный номер сертификата: %s.\n", s.Serial)
	}
	if info.Warning != "" {
		fmt.Fprintf(&b, "Предупреждение: %s\n", info.Warning)
	}
	fmt.Fprintf(&b, "ВНИМАНИЕ: %s.", CryptoNotice)
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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

// ExtractSignerSerials returns the serial of each signerInfo — i.e. the actual
// signatures, excluding the CA-chain certificates that also ride in the container.
func ExtractSignerSerials(printOut string) []string {
	var serials []string
	seen := map[string]bool{}
	lines := strings.Split(printOut, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "issuer_and_serial") {
			continue
		}
		for j := i + 1; j < len(lines) && j < i+8; j++ {
			if m := reSerialLine.FindStringSubmatch(lines[j]); m != nil {
				ser := NormalizeSerial(m[1])
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
	for _, block := range SplitPEMCerts(out.String()) {
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
			serial = NormalizeSerial(strings.TrimPrefix(line, "serial="))
		case strings.HasPrefix(line, "subject="):
			subject = DecodeOpenSSLEscapes(strings.TrimPrefix(line, "subject="))
		}
	}
	return serial, subject
}

// SplitPEMCerts cuts openssl's -print_certs output into certificate blocks.
func SplitPEMCerts(pem string) []string {
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

// NormalizeSerial strips the 0x prefix and upper-cases a certificate serial.
func NormalizeSerial(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	return strings.ToUpper(s)
}

var reHexEsc = regexp.MustCompile(`\\([0-9A-Fa-f]{2})`)

// DecodeOpenSSLEscapes turns openssl's \XX escapes for non-ASCII bytes back into
// readable UTF-8 (Cyrillic subjects are emitted byte-by-byte as \D0\9C ...).
func DecodeOpenSSLEscapes(s string) string {
	return reHexEsc.ReplaceAllStringFunc(s, func(m string) string {
		b, err := strconv.ParseUint(m[1:], 16, 8)
		if err != nil {
			return m
		}
		return string([]byte{byte(b)})
	})
}

// ParseSigner builds a Signer from a serial and an openssl subject line.
func ParseSigner(serial, subject string) Signer {
	s := Signer{Serial: serial}
	if subject == "" {
		s.Name = "(certificate not present in container)"
		return s
	}
	s.Name = SubjectAttr(subject, "CN")
	s.Org = SubjectAttr(subject, "O")
	s.Title = SubjectAttr(subject, "title")
	s.SNILS = SubjectAttr(subject, "SNILS")
	s.INN = SubjectAttr(subject, "INN")
	if v := SubjectAttr(subject, "1.2.643.100.5"); v != "" { // OGRNIP OID
		s.OGRNIP = v
	}
	return s
}

// SubjectAttr extracts the value of a single RDN (e.g. CN, O, SNILS) from an
// openssl subject string. The fields we read never contain commas, so a simple
// comma-terminated capture is sufficient.
func SubjectAttr(subject, key string) string {
	re := regexp.MustCompile(`(?:^|, )` + regexp.QuoteMeta(key) + ` = ([^,]+)`)
	m := re.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}
