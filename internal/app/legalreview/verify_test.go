package legalreview

import (
	"strings"
	"testing"
)

func TestVerifyFlagsInventedCadastralNumberAndArea(t *testing.T) {
	evidence := []string{
		"Участок 78:34:0004163:4729, площадь 29 439 +/- 36 кв. м. Общая площадь здания 118 020 м².",
	}
	report := `1. Два разных кадастровых номера: 78:34:0004163:4729 и 78:34:0004216:1725.
2. Площадь участка: 29 439 м² против 28 600 м².
3. Общая площадь здания 118 020 м² — совпадает.`

	bad := Verify(report, evidence)
	got := map[string]bool{}
	for _, u := range bad {
		got[u.Value] = true
	}
	if !got["78:34:0004216:1725"] {
		t.Errorf("invented cadastral number not flagged: %+v", bad)
	}
	if !got["28600"] {
		t.Errorf("invented area not flagged: %+v", bad)
	}
	if got["78:34:0004163:4729"] || got["29439"] || got["118020"] {
		t.Errorf("values present in the evidence must not be flagged: %+v", bad)
	}
}

// Numbers are written with thin spaces, non-breaking spaces and decimal commas;
// the same quantity must match across all of those spellings.
func TestVerifyNormalizesNumberSpelling(t *testing.T) {
	evidence := []string{"площадь застройки 16679 кв.м, высота 39.88 м, участок 29439"}
	report := "Площадь застройки 16\u00a0679 м², высота 39,88 м, участок 29 439,00 кв. м."
	if bad := Verify(report, evidence); len(bad) != 0 {
		t.Fatalf("same numbers in another spelling must verify: %+v", bad)
	}
}

// A norm the coordinator fetched with norm_search is evidence too: quantities
// quoted from it must not be reported as invented.
func TestVerifyAcceptsQuantitiesFromFetchedNorms(t *testing.T) {
	evidence := []string{"выжимка без чисел", "СП 42.13330: минимальный отступ составляет 3 м от границы участка"}
	if bad := Verify("Отступ по норме — 3 м, в проекте не показан.", evidence); len(bad) != 0 {
		t.Fatalf("norm quantities must verify: %+v", bad)
	}
}

// Years, article numbers and counts carry no unit and are not factual claims
// about the documents, so they are never checked.
func TestVerifyIgnoresNonQuantities(t *testing.T) {
	if bad := Verify("ГПЗУ от 20.08.2018, ст. 51 ч. 7 п. 2, всего 19 замечаний, расхождение в 59 раз.", []string{"пусто"}); len(bad) != 0 {
		t.Fatalf("non-quantities must not be checked: %+v", bad)
	}
}

func TestVerifyEmptyInputs(t *testing.T) {
	if bad := Verify("", []string{"x"}); bad != nil {
		t.Error("empty report yields nothing to check")
	}
	if bad := Verify("площадь 500 м²", nil); bad != nil {
		t.Error("without evidence nothing can be judged invented")
	}
}

func TestRenderUnverified(t *testing.T) {
	out := RenderUnverified([]Unverified{
		{Value: "28600", Kind: "величина", Quote: "28 600 м²"},
		{Value: "78:34:0004216:1725", Kind: "кадастровый номер", Quote: "78:34:0004216:1725"},
	})
	for _, want := range []string{"НЕ ПОДТВЕРЖДЕНО", "28 600 м²", "78:34:0004216:1725", "кадастровый номер"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered block lacks %q:\n%s", want, out)
		}
	}
	if RenderUnverified(nil) != "" {
		t.Error("nothing unverified renders to an empty string")
	}
}
