package norms

import (
	"strings"
	"testing"
)

const law218 = `Федеральный закон от 13.07.2015 N 218-ФЗ
О государственной регистрации недвижимости

Статья 26. Основания и сроки приостановления осуществления государственного кадастрового учета
1. Осуществление государственного кадастрового учета и (или) государственной регистрации прав приостанавливается по решению государственного регистратора прав в случае, если:
1) лицо, указанное в заявлении в качестве правообладателя, не имеет права на такой объект недвижимости;
2) с заявлением о государственном кадастровом учете обратилось ненадлежащее лицо;
7) форма и (или) содержание документа, представленного для осуществления государственного кадастрового учета, не соответствуют требованиям законодательства Российской Федерации;
2. Осуществление государственного кадастрового учета приостанавливается на срок до устранения причин.
Статья 27. Отказ в осуществлении государственного кадастрового учета
В осуществлении государственного кадастрового учета отказывается по решению государственного регистратора прав в случае, если в течение срока приостановления не устранены причины.
`

const sp4 = `СП 4.13130.2013 Системы противопожарной защиты
1 Область применения
1.1 Настоящий свод правил устанавливает требования пожарной безопасности к объемно-планировочным решениям.
4 Общие требования
4.3 Противопожарные расстояния между жилыми, общественными зданиями следует принимать по таблице 1.
4.3.1 Расстояния между зданиями определяются как расстояние между наружными стенами.
6.1.2 Производственные здания категорий А и Б следует размещать с учетом розы ветров.
Приложение А
Сведения о категориях зданий по взрывопожарной опасности.
`

func refsOf(chunks []Chunk) []string {
	var out []string
	for _, c := range chunks {
		out = append(out, c.Ref)
	}
	return out
}

func find(t *testing.T, chunks []Chunk, ref string) Chunk {
	t.Helper()
	for _, c := range chunks {
		if c.Ref == ref {
			return c
		}
	}
	t.Fatalf("ref %q not found; have %v", ref, refsOf(chunks))
	return Chunk{}
}

func TestSplit_LawArticlesPartsPoints(t *testing.T) {
	chunks := Split(law218)

	pre := find(t, chunks, preambleRef)
	if !strings.Contains(pre.Text, "218-ФЗ") {
		t.Fatalf("preamble should carry the title, got %q", pre.Text)
	}
	art := find(t, chunks, "ст. 26")
	if !strings.Contains(art.Text, "Основания и сроки") {
		t.Fatalf("article header lost: %q", art.Text)
	}
	part := find(t, chunks, "ст. 26 ч. 1")
	if !strings.Contains(part.Text, "приостанавливается по решению") {
		t.Fatalf("part 1 text: %q", part.Text)
	}
	p7 := find(t, chunks, "ст. 26 ч. 1 п. 7")
	if !strings.Contains(p7.Text, "форма и (или) содержание") {
		t.Fatalf("point 7 text: %q", p7.Text)
	}
	find(t, chunks, "ст. 26 ч. 2")
	art27 := find(t, chunks, "ст. 27")
	if !strings.Contains(art27.Text, "отказывается") {
		// Article body without numbered parts stays with the article.
		t.Fatalf("article 27 body: %q", art27.Text)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Ord != chunks[i-1].Ord+1 {
			t.Fatalf("ord not sequential at %d: %v", i, chunks)
		}
	}
}

func TestSplit_CodeOfRulesClauses(t *testing.T) {
	chunks := Split(sp4)

	sec := find(t, chunks, "п. 1")
	if !strings.Contains(sec.Text, "Область применения") {
		t.Fatalf("section heading: %q", sec.Text)
	}
	c11 := find(t, chunks, "п. 1.1")
	if strings.Contains(c11.Text, "Общие требования") {
		t.Fatalf("next section glued to 1.1: %q", c11.Text)
	}
	find(t, chunks, "п. 4")
	c := find(t, chunks, "п. 4.3")
	if !strings.Contains(c.Text, "Противопожарные расстояния") || strings.Contains(c.Text, "4.3.1") {
		t.Fatalf("clause 4.3 should end before 4.3.1: %q", c.Text)
	}
	find(t, chunks, "п. 4.3.1")
	find(t, chunks, "п. 6.1.2")
	app := find(t, chunks, "Приложение А")
	if !strings.Contains(app.Text, "категориях") {
		t.Fatalf("appendix text: %q", app.Text)
	}
}

func TestSplit_LongUnitIsPagedKeepingRef(t *testing.T) {
	sentence := "Это длинное предложение нормативного текста, которое повторяется. "
	long := "Статья 5. Длинная\n1. " + strings.Repeat(sentence, 80)
	chunks := Split(long)

	var pieces []Chunk
	for _, c := range chunks {
		if c.Ref == "ст. 5 ч. 1" {
			pieces = append(pieces, c)
		}
	}
	if len(pieces) < 2 {
		t.Fatalf("expected the part to be split, got %d piece(s)", len(pieces))
	}
	for _, p := range pieces {
		if len(p.Text) > maxChunkChars {
			t.Fatalf("piece over budget: %d chars", len(p.Text))
		}
		if !strings.HasSuffix(strings.TrimSpace(p.Text), ".") {
			t.Fatalf("piece should end at a sentence boundary: %q", p.Text[len(p.Text)-30:])
		}
	}
}

func TestSplit_EmptyText(t *testing.T) {
	if got := Split("  \n\n "); len(got) != 0 {
		t.Fatalf("expected no chunks, got %v", got)
	}
}

func TestSplitLong_HardCutOnRuneBoundary(t *testing.T) {
	text := strings.Repeat("я", 1000) // no spaces, no sentence ends
	for _, p := range splitLong(text, 301) {
		if !isRuneStart(p[0]) || len(p) > 301 {
			t.Fatalf("bad piece: len=%d", len(p))
		}
		if strings.ContainsRune(p, '�') {
			t.Fatalf("rune was cut in half")
		}
	}
}
