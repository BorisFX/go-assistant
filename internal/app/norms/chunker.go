package norms

import (
	"regexp"
	"strings"
)

// maxChunkChars bounds one chunk. A long article part is split at sentence
// boundaries; every piece keeps the same Ref so a citation still names the
// unit the text belongs to.
const maxChunkChars = 1800

// preambleRef labels text that precedes the first numbered unit (title,
// scope line, table of contents).
const preambleRef = "преамбула"

var (
	reArticle  = regexp.MustCompile(`^\s*Статья\s+(\d+(?:\.\d+)*)\.?`)
	reAppendix = regexp.MustCompile(`^\s*Приложение\s+([А-ЯЁA-Z0-9]+)`)
	// "1. текст" — a part inside an article, or a section in a code of rules.
	rePart = regexp.MustCompile(`^\s*(\d+)\.\s+\S`)
	// "7) текст" — a point inside a part.
	rePoint = regexp.MustCompile(`^\s*(\d+)\)\s+\S`)
	// "4.3 текст" / "6.1.2. текст" — a numbered clause of a СП/ГОСТ.
	reClause = regexp.MustCompile(`^\s*(\d+(?:\.\d+)+)\.?\s+\S`)
	// "4 Общие требования" — a section heading of a СП/ГОСТ (no dot after
	// the number, title in capitals). Only meaningful outside a law's articles.
	reSection = regexp.MustCompile(`^\s*(\d+)\s+[А-ЯЁ]`)
	// Sentence ends: a period/semicolon followed by whitespace.
	reSentenceEnd = regexp.MustCompile(`[.;]\s`)
)

// Split cuts a document's plain text into chunks by legal unit. Articles,
// parts and points (laws) and dotted clauses (building codes) each start a
// unit; the text before the first unit becomes the preamble.
func Split(text string) []Chunk {
	var (
		units   []unit
		cur     = unit{ref: preambleRef}
		article string // current "ст. N", empty outside an article
		part    string // current part number inside the article
	)
	flush := func() {
		if strings.TrimSpace(cur.text.String()) != "" {
			units = append(units, cur)
		}
	}
	start := func(ref string) {
		flush()
		cur = unit{ref: ref}
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		switch {
		case reArticle.MatchString(line):
			article = "ст. " + reArticle.FindStringSubmatch(line)[1]
			part = ""
			start(article)
		case reAppendix.MatchString(line):
			article, part = "", ""
			start("Приложение " + reAppendix.FindStringSubmatch(line)[1])
		case reClause.MatchString(line):
			// Dotted numbering belongs to codes of rules; a law never has it.
			article, part = "", ""
			start("п. " + reClause.FindStringSubmatch(line)[1])
		case article == "" && reSection.MatchString(line):
			start("п. " + reSection.FindStringSubmatch(line)[1])
		case rePart.MatchString(line):
			num := rePart.FindStringSubmatch(line)[1]
			if article != "" {
				part = num
				start(article + " ч. " + num)
			} else {
				start("п. " + num)
			}
		case rePoint.MatchString(line):
			num := rePoint.FindStringSubmatch(line)[1]
			switch {
			case article != "" && part != "":
				start(article + " ч. " + part + " п. " + num)
			case article != "":
				start(article + " п. " + num)
			default:
				start(cur.baseRef() + " пп. " + num)
			}
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if cur.text.Len() > 0 {
			cur.text.WriteByte('\n')
		}
		cur.text.WriteString(strings.TrimSpace(line))
	}
	flush()

	var out []Chunk
	ord := 0
	for _, u := range units {
		for _, piece := range splitLong(u.text.String(), maxChunkChars) {
			out = append(out, Chunk{Ref: u.ref, Ord: ord, Text: piece})
			ord++
		}
	}
	return out
}

type unit struct {
	ref  string
	text strings.Builder
}

// baseRef strips a trailing " пп. N" so nested points under a clause do not
// accumulate ("п. 4.3 пп. 1", not "п. 4.3 пп. 1 пп. 2").
func (u unit) baseRef() string {
	if i := strings.Index(u.ref, " пп. "); i >= 0 {
		return u.ref[:i]
	}
	return u.ref
}

// splitLong cuts text into pieces of at most max chars, preferring a sentence
// boundary in the second half of the window so pieces stay readable.
func splitLong(text string, max int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return []string{text}
	}
	var pieces []string
	for len(text) > max {
		cut := -1
		window := text[:max]
		for _, m := range reSentenceEnd.FindAllStringIndex(window, -1) {
			if m[1] > max/2 {
				cut = m[1]
			}
		}
		if cut < 0 {
			// No sentence end: fall back to the last space, then to a hard cut
			// on a rune boundary.
			cut = strings.LastIndex(window, " ")
			if cut < max/2 {
				cut = max
				for cut > 0 && !isRuneStart(text[cut]) {
					cut--
				}
			}
		}
		pieces = append(pieces, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		pieces = append(pieces, text)
	}
	return pieces
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
