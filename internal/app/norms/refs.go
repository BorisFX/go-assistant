package norms

import (
	"regexp"
	"strings"
)

// RefQuery is a citation found in free text: which document and which unit.
// An empty Ref means the document was named without a unit.
type RefQuery struct {
	DocCode string
	Ref     string
}

// Go's regexp is RE2: \b and \w are ASCII-only, so Cyrillic word boundaries
// are spelled out as "start of text or a non-letter" and letters as [а-яё].
const (
	guard  = `(^|[^а-яёa-z0-9])`
	letter = `[а-яё]`
)

var (
	reUnitWord = regexp.MustCompile(`(?i)` + guard + `(статьями|статьей|статьёй|статьи|статье|статья|ст)\.?\s*(\d)`)
	rePartWord = regexp.MustCompile(`(?i)` + guard + `(частями|частью|части|часть|ч)\.?\s*(\d)`)
	rePntWord  = regexp.MustCompile(`(?i)` + guard + `(подпунктами|подпунктом|подпункта|подпункт|пп)\.?\s*(\d)`)
	rePointW   = regexp.MustCompile(`(?i)` + guard + `(пунктами|пунктом|пункта|пункте|пункт|п)\.?\s*(\d)`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// NormalizeRef folds a unit reference to one spelling so "ст.26 ч.1 п.7",
// "статья 26 часть 1 пункт 7" and "ст. 26 ч. 1 п. 7" all compare equal.
func NormalizeRef(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = rePntWord.ReplaceAllString(s, "${1}пп. ${3}")
	s = rePointW.ReplaceAllString(s, "${1}п. ${3}")
	s = rePartWord.ReplaceAllString(s, "${1}ч. ${3}")
	s = reUnitWord.ReplaceAllString(s, "${1}ст. ${3}")
	s = reSpaces.ReplaceAllString(s, " ")
	s = strings.TrimRight(s, ". ")
	return strings.TrimSpace(s)
}

// NormalizeDocCode folds a document code for prefix matching: "СП 4.13130"
// matches "СП 4.13130.2013", "ГрК" matches "ГрК РФ".
func NormalizeDocCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.ReplaceAll(s, "№", "")
	s = strings.ReplaceAll(s, " ", "")
	return strings.TrimRight(s, ".")
}

// Citation patterns. Laws are cited unit-first ("п. 7 ч. 1 ст. 26 218-ФЗ") or
// article-first ("ст. 26 ч. 1 п. 7 218-ФЗ"); codes of rules either way
// ("п. 4.3 СП 4.13130.2013" or "СП 4.13130.2013 п. 4.3").
var (
	lawCodes = `(218-ФЗ|190-ФЗ|384-ФЗ|123-ФЗ|214-ФЗ|ГрК(?:\s*РФ)?|ЗК(?:\s*РФ)?|ГК(?:\s*РФ)?|Градостроительн` + letter + `+\s+кодекс` + letter + `*|Земельн` + letter + `+\s+кодекс` + letter + `*)`
	unitPart = `(?:(?:ч\.?|част` + letter + `+)\s*(\d+))?`
	unitPnt  = `(?:(?:п\.?|пункт` + letter + `*)\s*(\d+(?:\.\d+)*))?`
	unitArt  = `(?:ст\.?|стать` + letter + `+)\s*(\d+(?:\.\d+)?)`
	lawTail  = `\s*(?:Федерального\s+закона\s*)?(?:№\s*)?`

	// guard + ст. 26 [ч. 1] [п. 7] 218-ФЗ  → groups: 1 guard, 2 art, 3 part, 4 pnt, 5 code
	reLawArtFirst = regexp.MustCompile(`(?i)` + guard + unitArt + `\s*` + unitPart + `\s*` + unitPnt + lawTail + lawCodes)
	// guard + [п. 7] [ч. 1] ст. 26 218-ФЗ  → groups: 1 guard, 2 pnt, 3 part, 4 art, 5 code
	reLawUnitFirst = regexp.MustCompile(`(?i)` + guard + unitPnt + `\s*` + unitPart + `\s*` + unitArt + lawTail + lawCodes)

	codeNames = `(СП|СНиП|СанПиН|ГОСТ\s*Р?)\s*(\d+(?:[.\-]\d+)*)`
	// guard + п. 4.3 СП 4.13130.2013 → 1 guard, 2 pnt, 3 kind, 4 number
	reCodePntFirst = regexp.MustCompile(`(?i)` + guard + `(?:п\.?|пункт` + letter + `*)\s*(\d+(?:\.\d+)+)\s*,?\s*` + codeNames)
	// guard + СП 4.13130.2013[,] п. 4.3 → 1 guard, 2 kind, 3 number, 4 pnt
	reCodeDocFirst = regexp.MustCompile(`(?i)` + guard + codeNames + `\s*,?\s*(?:п\.?|пункт` + letter + `*)\s*(\d+(?:\.\d+)+)`)
	// Bare document mention → 1 guard, 2 kind, 3 number
	reCodeBare = regexp.MustCompile(`(?i)` + guard + codeNames)
	reDecree87 = regexp.MustCompile(`(?i)постановлени` + letter + `*\s+правительства[^.;\n]{0,60}?№?\s*87(?:[^0-9]|$)`)
)

// ExtractRefs finds citations in free text. Results are de-duplicated and
// keep first-seen order, so the most prominent references come first.
func ExtractRefs(text string) []RefQuery {
	var out []RefQuery
	seen := map[string]bool{}
	add := func(q RefQuery) {
		q.DocCode = canonicalLaw(q.DocCode)
		q.Ref = NormalizeRef(q.Ref)
		k := NormalizeDocCode(q.DocCode) + "|" + q.Ref
		if q.DocCode == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, q)
	}

	// Unit-first citations ("п. 7 ч. 1 ст. 26 218-ФЗ") contain an article-first
	// suffix ("ст. 26 218-ФЗ"); the specific match is taken and its span masked
	// so the loose pattern does not also yield the bare article.
	for _, m := range reLawUnitFirst.FindAllStringSubmatch(text, -1) {
		add(RefQuery{DocCode: m[5], Ref: lawRef(m[4], m[3], m[2])})
	}
	masked := reLawUnitFirst.ReplaceAllStringFunc(text, blank)
	for _, m := range reLawArtFirst.FindAllStringSubmatch(masked, -1) {
		add(RefQuery{DocCode: m[5], Ref: lawRef(m[2], m[3], m[4])})
	}
	for _, m := range reCodePntFirst.FindAllStringSubmatch(text, -1) {
		add(RefQuery{DocCode: codeName(m[3], m[4]), Ref: "п. " + m[2]})
	}
	for _, m := range reCodeDocFirst.FindAllStringSubmatch(text, -1) {
		add(RefQuery{DocCode: codeName(m[2], m[3]), Ref: "п. " + m[4]})
	}
	if reDecree87.MatchString(text) {
		add(RefQuery{DocCode: "ПП 87"})
	}
	for _, m := range reCodeBare.FindAllStringSubmatch(text, -1) {
		add(RefQuery{DocCode: codeName(m[2], m[3])})
	}
	return out
}

// blank replaces a match with spaces of the same byte length, keeping the
// rest of the text in place for the next pattern.
func blank(m string) string { return strings.Repeat(" ", len(m)) }

func lawRef(article, part, point string) string {
	ref := "ст. " + article
	if part != "" {
		ref += " ч. " + part
	}
	if point != "" {
		ref += " п. " + point
	}
	return ref
}

func codeName(kind, number string) string {
	kind = strings.ToUpper(reSpaces.ReplaceAllString(kind, ""))
	switch kind {
	case "ГОСТР":
		kind = "ГОСТ Р"
	case "САНПИН":
		kind = "СанПиН"
	case "СНИП":
		kind = "СНиП"
	}
	return kind + " " + number
}

// canonicalLaw maps the spellings a citation may use to the corpus code.
func canonicalLaw(code string) string {
	c := strings.ToLower(reSpaces.ReplaceAllString(strings.TrimSpace(code), " "))
	switch {
	case strings.HasPrefix(c, "градостроительн"), strings.HasPrefix(c, "грк"), c == "190-фз":
		return "ГрК РФ"
	case strings.HasPrefix(c, "земельн"), strings.HasPrefix(c, "зк"):
		return "ЗК РФ"
	case strings.HasPrefix(c, "гк"):
		return "ГК РФ"
	}
	return strings.ToUpper(strings.TrimSpace(code))
}
