package legalreview

import (
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

func TestCollide_NeedsTwoDocumentsWithFacts(t *testing.T) {
	if out := Collide(nil); out != "" {
		t.Fatalf("nil digests must give empty block")
	}
	one := []Digest{
		{Path: "/d/a.pdf", Facts: Facts{DocType: "техплан", TEP: TEP{AreaTotalM2: fp(100)}}},
		{Path: "/d/b.pdf", Text: "no facts"},
	}
	if out := Collide(one); out != "" {
		t.Fatalf("one document with facts must give empty block, got %q", out)
	}
}

func TestCollide_EqualValuesWithinTolerance(t *testing.T) {
	digests := []Digest{
		{Path: "/d/техплан.xml", Facts: Facts{DocType: "техплан", CadastralNumbers: []string{"50:16:0000000:1"}, TEP: TEP{AreaTotalM2: fp(3317.6), Floors: fp(1)}}},
		{Path: "/d/РнС.pdf", Facts: Facts{DocType: "разрешение на строительство", CadastralNumbers: []string{"50:16:0000000:1"}, TEP: TEP{AreaTotalM2: fp(3317.55), Floors: fp(1)}}},
	}
	out := Collide(digests)
	if !strings.HasPrefix(out, "АВТОСВЕРКА") {
		t.Fatalf("block must start with header: %q", out)
	}
	// The legend in the header names the glyph; only the body counts.
	body := out[strings.Index(out, "\n\n"):]
	if strings.Contains(body, "🔴") {
		t.Fatalf("0.05 m² is rounding, not a collision:\n%s", out)
	}
	for _, want := range []string{"✅ Общая площадь", "✅ Этажность", "✅ Кадастровые номера", "техплан.xml [техплан]", "РнС.pdf [разрешение на строительство]", "3317.6", "3317.55"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCollide_FlagsDifferences(t *testing.T) {
	digests := []Digest{
		{Path: "/d/АР.pdf", Facts: Facts{DocType: "проектная документация", Address: "МО, Ногинск, ул. Ленина, 1", TEP: TEP{AreaBuildingM2: fp(1450), Floors: fp(2)}}},
		{Path: "/d/СПОЗУ.pdf", Facts: Facts{DocType: "проектная документация", Address: "МО, г. Ногинск, ул. Ленина, д. 1", TEP: TEP{AreaBuildingM2: fp(1420), Floors: fp(3), HeightM: fp(12)}}},
		{Path: "/d/ЕГРН.pdf", Facts: Facts{DocType: "выписка ЕГРН", CadastralNumbers: []string{"50:16:1:1"}}},
		{Path: "/d/техплан.xml", Facts: Facts{DocType: "техплан", CadastralNumbers: []string{"50:16:1:2"}}},
	}
	out := Collide(digests)
	for _, want := range []string{"🔴 Площадь здания", "🔴 Этажность", "🔴 Кадастровые номера", "Высота, м: 12 (СПОЗУ.pdf [проектная документация]) — нет сверки", "🔴 Адрес"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Строительный объём") {
		t.Fatalf("fields nobody states must not appear:\n%s", out)
	}
}

func TestCollide_ExactFieldsAreStrict(t *testing.T) {
	digests := []Digest{
		{Path: "/d/a.pdf", Facts: Facts{DocType: "техплан", TEP: TEP{FloorsCount: fp(2)}}},
		{Path: "/d/b.pdf", Facts: Facts{DocType: "ГПЗУ", TEP: TEP{FloorsCount: fp(3)}}},
	}
	if out := Collide(digests); !strings.Contains(out, "🔴 Количество этажей") {
		t.Fatalf("floor counts 2 vs 3 must be flagged:\n%s", out)
	}
}

func TestCollide_TenthOfSquareMetreIsACollision(t *testing.T) {
	// Cadastral records keep 0.1 m²: 3317.6 vs 3317.5 is what a registrar
	// flags, so the auto-check must not hide it behind a relative tolerance.
	digests := []Digest{
		{Path: "/d/техплан.xml", Facts: Facts{DocType: "техплан", TEP: TEP{AreaTotalM2: fp(3317.6)}}},
		{Path: "/d/РнС.pdf", Facts: Facts{DocType: "разрешение на строительство", TEP: TEP{AreaTotalM2: fp(3317.5)}}},
	}
	out := Collide(digests)
	if !strings.Contains(out, "🔴 Общая площадь") {
		t.Fatalf("expected a collision on 0.1 m²:\n%s", out)
	}
}
