package legalreview

import (
	"regexp"
	"strings"
)

// Unverified is one factual claim in the report that no source document backs.
type Unverified struct {
	Value string // canonical form, e.g. "28600"
	Kind  string // "кадастровый номер" | "величина"
	Quote string // as written in the report
}

var (
	// cadastralRe matches a Russian cadastral number: 78:34:0004163:4729.
	cadastralRe = regexp.MustCompile(`\d{2}:\d{2}:\d{6,7}:\d+`)
	// quantityRe matches a number carrying a unit of area, volume or length.
	// Only such numbers are factual claims about the documents; years, article
	// numbers and counts carry no unit and are none of the verifier's business.
	quantityRe = regexp.MustCompile(`(\d[\d\s\x{00a0}\x{202f}]*(?:[.,]\d+)?)\s*(?:м²|м2|м³|м3|кв\.?\s*м|куб\.?\s*м|га|м)(?:[^а-яё]|$)`)
	// numberRe finds every number in the evidence, unit or not: the same value
	// is often written without a unit in the source it was taken from.
	numberRe = regexp.MustCompile(`\d[\d\s\x{00a0}\x{202f}]*(?:[.,]\d+)?`)
)

// Verify checks the coordinator's report against the evidence it was shown and
// returns the claims that occur nowhere in it.
//
// Two classes of claim are machine-checkable, and they are exactly the two a
// production run got wrong: identifiers copied from documents (cadastral
// numbers) and quantities (areas, volumes, heights). A model that invents one
// of these and attributes it to a document produces a report that reads as
// authoritative and is false, so the check is worth its false-positive cost:
// an unbacked value is reported, never silently removed.
//
// Evidence is everything the coordinator actually read: the digests, the
// deterministic cross-check, the normative excerpts and whatever norm_search
// returned. Without evidence nothing can be judged invented.
func Verify(report string, evidence []string) []Unverified {
	if strings.TrimSpace(report) == "" || len(evidence) == 0 {
		return nil
	}

	known := map[string]bool{}
	for _, e := range evidence {
		for _, m := range cadastralRe.FindAllString(e, -1) {
			known[m] = true
		}
		for _, m := range numberRe.FindAllString(e, -1) {
			known[canonNumber(m)] = true
		}
	}

	seen := map[string]bool{}
	var out []Unverified
	add := func(value, kind, quote string) {
		if value == "" || known[value] || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, Unverified{Value: value, Kind: kind, Quote: strings.TrimSpace(quote)})
	}

	for _, m := range cadastralRe.FindAllString(report, -1) {
		add(m, "кадастровый номер", m)
	}
	for _, m := range quantityRe.FindAllStringSubmatch(report, -1) {
		add(canonNumber(m[1]), "величина", m[0])
	}
	return out
}

// canonNumber strips the separators a number may be written with (thin and
// non-breaking spaces, thousands spaces), unifies the decimal mark and drops
// trailing zeros, so «29 439,00», «29 439» and «29439» are one value.
func canonNumber(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', ' ', ' ', '\t':
		case ',':
			b.WriteRune('.')
		default:
			b.WriteRune(r)
		}
	}
	n := b.String()
	if strings.Contains(n, ".") {
		n = strings.TrimRight(n, "0")
		n = strings.TrimSuffix(n, ".")
	}
	return n
}

// RenderUnverified turns the findings into the report block that warns the
// reader which numbers no document backs.
func RenderUnverified(list []Unverified) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("НЕ ПОДТВЕРЖДЕНО ИСТОЧНИКАМИ (проверка выполнена программой)\n")
	b.WriteString("Эти значения есть в тексте заключения, но их нет ни в одном прочитанном документе и ни в одной процитированной норме. До проверки по оригиналу считайте их ошибкой модели, а не фактом.\n\n")
	for _, u := range list {
		b.WriteString("• " + u.Quote + " (" + u.Kind + ")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
