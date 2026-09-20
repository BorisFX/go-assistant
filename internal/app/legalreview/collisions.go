package legalreview

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// numericTolerance is the relative difference below which two areas or volumes
// are the same number written with different rounding (0.5%).
const numericTolerance = 0.005

// tepField describes one comparable numeric parameter.
type tepField struct {
	label string
	get   func(TEP) *float64
	exact bool // floors and counts must match exactly; areas within tolerance
}

var tepFields = []tepField{
	{"Площадь застройки, м²", func(t TEP) *float64 { return t.AreaFootprintM2 }, false},
	{"Общая площадь, м²", func(t TEP) *float64 { return t.AreaTotalM2 }, false},
	{"Площадь здания, м²", func(t TEP) *float64 { return t.AreaBuildingM2 }, false},
	{"Этажность", func(t TEP) *float64 { return t.Floors }, true},
	{"Количество этажей", func(t TEP) *float64 { return t.FloorsCount }, true},
	{"Высота, м", func(t TEP) *float64 { return t.HeightM }, false},
	{"Строительный объём, м³", func(t TEP) *float64 { return t.VolumeM3 }, false},
	{"Площадь участка, м²", func(t TEP) *float64 { return t.LandAreaM2 }, false},
}

// Collide cross-checks the structured facts of a batch and renders the result
// as a Russian text block. It is deterministic Go, not a model: the numbers
// either agree or they do not, and the coordinator gets that as a fact.
// Returns "" when fewer than two documents carry facts.
func Collide(digests []Digest) string {
	var docs []Digest
	for _, d := range digests {
		if !d.Facts.Empty() {
			docs = append(docs, d)
		}
	}
	if len(docs) < 2 {
		return ""
	}

	var b strings.Builder
	b.WriteString("АВТОСВЕРКА (детерминированная, по извлечённым данным)\n")
	b.WriteString("Сравнение ключевых параметров между документами пачки. 🔴 — расхождение, ✅ — совпадает, «нет сверки» — параметр есть только в одном документе.\n\n")

	for _, f := range tepFields {
		var have []Digest
		for _, d := range docs {
			if f.get(d.Facts.TEP) != nil {
				have = append(have, d)
			}
		}
		if len(have) == 0 {
			continue
		}
		if len(have) == 1 {
			fmt.Fprintf(&b, "• %s: %s (%s) — нет сверки\n", f.label, fmtNum(*f.get(have[0].Facts.TEP)), docLabel(have[0]))
			continue
		}
		mark := "✅"
		if numbersDiffer(have, f) {
			mark = "🔴"
		}
		fmt.Fprintf(&b, "%s %s:\n", mark, f.label)
		for _, d := range have {
			fmt.Fprintf(&b, "    %s: %s\n", docLabel(d), fmtNum(*f.get(d.Facts.TEP)))
		}
	}

	writeSetField(&b, "Кадастровые номера", docs, func(f Facts) []string { return f.CadastralNumbers })
	writeStringField(&b, "Адрес", docs, func(f Facts) string { return f.Address })

	return strings.TrimRight(b.String(), "\n")
}

// numbersDiffer reports whether any two documents disagree on the field.
func numbersDiffer(have []Digest, f tepField) bool {
	base := *f.get(have[0].Facts.TEP)
	for _, d := range have[1:] {
		v := *f.get(d.Facts.TEP)
		if f.exact {
			if v != base {
				return true
			}
			continue
		}
		scale := math.Max(math.Abs(base), math.Abs(v))
		if scale == 0 {
			if v != base {
				return true
			}
			continue
		}
		if math.Abs(v-base)/scale > numericTolerance {
			return true
		}
	}
	return false
}

// writeSetField compares list-valued fields (cadastral numbers): documents
// disagree when their sets differ.
func writeSetField(b *strings.Builder, label string, docs []Digest, get func(Facts) []string) {
	var have []Digest
	for _, d := range docs {
		if len(get(d.Facts)) > 0 {
			have = append(have, d)
		}
	}
	if len(have) == 0 {
		return
	}
	if len(have) == 1 {
		fmt.Fprintf(b, "• %s: %s (%s) — нет сверки\n", label, strings.Join(get(have[0].Facts), ", "), docLabel(have[0]))
		return
	}
	mark := "✅"
	base := normalizedSet(get(have[0].Facts))
	for _, d := range have[1:] {
		if normalizedSet(get(d.Facts)) != base {
			mark = "🔴"
			break
		}
	}
	fmt.Fprintf(b, "%s %s:\n", mark, label)
	for _, d := range have {
		fmt.Fprintf(b, "    %s: %s\n", docLabel(d), strings.Join(get(d.Facts), ", "))
	}
}

// writeStringField compares free-text fields (address) after light
// normalization; a mismatch is flagged but is a hint, not a verdict.
func writeStringField(b *strings.Builder, label string, docs []Digest, get func(Facts) string) {
	var have []Digest
	for _, d := range docs {
		if strings.TrimSpace(get(d.Facts)) != "" {
			have = append(have, d)
		}
	}
	if len(have) < 2 {
		return
	}
	mark := "✅"
	base := normalizeText(get(have[0].Facts))
	for _, d := range have[1:] {
		if normalizeText(get(d.Facts)) != base {
			mark = "🔴"
			break
		}
	}
	fmt.Fprintf(b, "%s %s (сверка по тексту, требует прочтения):\n", mark, label)
	for _, d := range have {
		fmt.Fprintf(b, "    %s: %s\n", docLabel(d), get(d.Facts))
	}
}

func normalizedSet(in []string) string {
	set := make([]string, 0, len(in))
	for _, s := range in {
		set = append(set, normalizeText(s))
	}
	sort.Strings(set)
	return strings.Join(set, "|")
}

func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ё", "е", ".", "", ",", "", " ", "", "\u00a0", "", "-", "", "«", "", "»", "", "\"", "").Replace(s)
	return s
}

// docLabel names a document by file name and detected type, short enough for
// one line per document in the comparison block.
func docLabel(d Digest) string {
	name := filepath.Base(d.Path)
	if d.Facts.DocType != "" && d.Facts.DocType != "прочее" {
		return fmt.Sprintf("%s [%s]", name, d.Facts.DocType)
	}
	return name
}

// fmtNum prints a parameter without trailing zeros: 1450.5 → "1450.5", 3 → "3".
func fmtNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
