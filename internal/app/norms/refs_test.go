package norms

import (
	"reflect"
	"testing"
)

func TestNormalizeRef(t *testing.T) {
	cases := map[string]string{
		"ст.26 ч.1 п.7":             "ст. 26 ч. 1 п. 7",
		"Статья 26 часть 1 пункт 7": "ст. 26 ч. 1 п. 7",
		"ст. 26 ч. 1 п. 7":          "ст. 26 ч. 1 п. 7",
		"  СТ. 51   Ч. 7 ":          "ст. 51 ч. 7",
		"п.4.3":                     "п. 4.3",
		"пункт 4.3.":                "п. 4.3",
		"подпункт 2 пункта 1 статьи 26": "пп. 2 п. 1 ст. 26",
		"пп. 2":        "пп. 2",
		"Приложение А": "приложение а",
		"":             "",
	}
	for in, want := range cases {
		if got := NormalizeRef(in); got != want {
			t.Errorf("NormalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDocCode(t *testing.T) {
	if NormalizeDocCode("СП 4.13130") != "сп4.13130" {
		t.Fatal(NormalizeDocCode("СП 4.13130"))
	}
	if NormalizeDocCode("ГрК РФ") != "гркрф" || NormalizeDocCode("№ 87") != "87" {
		t.Fatal("doc code folding")
	}
}

func TestExtractRefs(t *testing.T) {
	text := `Регистратор ссылается на п. 7 ч. 1 ст. 26 218-ФЗ и на ст. 51 ч. 7 ГрК РФ.
Также указано требование п. 4.3 СП 4.13130.2013 и СП 42.13330.2016, п. 7.1.
Состав разделов установлен постановлением Правительства РФ № 87.
Повтор: статья 26 часть 1 пункт 7 Федерального закона № 218-ФЗ.`

	got := ExtractRefs(text)
	want := []RefQuery{
		{DocCode: "218-ФЗ", Ref: "ст. 26 ч. 1 п. 7"},
		{DocCode: "ГрК РФ", Ref: "ст. 51 ч. 7"},
		{DocCode: "СП 4.13130.2013", Ref: "п. 4.3"},
		{DocCode: "СП 42.13330.2016", Ref: "п. 7.1"},
		{DocCode: "ПП 87", Ref: ""},
		{DocCode: "СП 4.13130.2013", Ref: ""},
		{DocCode: "СП 42.13330.2016", Ref: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractRefs:\n got  %+v\n want %+v", got, want)
	}
}

func TestExtractRefs_NoFalsePositivesInPlainProse(t *testing.T) {
	got := ExtractRefs("Площадь здания 1450 кв.м, спор о сроках, 87 листов, статья расходов.")
	if len(got) != 0 {
		t.Fatalf("expected no refs, got %+v", got)
	}
}

func TestExtractRefs_LawSpelledOut(t *testing.T) {
	got := ExtractRefs("в силу ч. 7 ст. 51 Градостроительного кодекса РФ")
	if len(got) != 1 || got[0].DocCode != "ГрК РФ" || got[0].Ref != "ст. 51 ч. 7" {
		t.Fatalf("got %+v", got)
	}
}
