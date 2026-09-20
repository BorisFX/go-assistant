package docgen

import (
	"context"
	"strings"
	"testing"
)

// The paper goes to a stranger, so structure has to survive: headings, lists
// and tables, not one flat blob of text.
func TestRenderHTMLKeepsStructure(t *testing.T) {
	out := RenderHTML("ТЗ.pdf", `# Техническое задание

## 1. Объект
Пристройка 220 м², д. Пирогово.

- геодезия
- геология

| Вид работ | Срок |
|---|---|
| Геодезия | 20 р.д. |

Текст с **важным** фрагментом.`)

	for _, want := range []string{
		"<h1>Техническое задание</h1>",
		"<h2>1. Объект</h2>",
		"<li>геодезия</li>",
		"<table>",
		"<td>Геодезия</td>",
		"<b>важным</b>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в HTML нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<td>---</td>") {
		t.Error("разделитель таблицы попал в документ")
	}
}

// Contractor text is not markup: angle brackets must not break the document.
func TestRenderHTMLEscapesText(t *testing.T) {
	out := RenderHTML("x.pdf", "Смета <не менее> 100 000 ₽ & НДС")

	if strings.Contains(out, "<не менее>") {
		t.Errorf("текст не экранирован: %s", out)
	}
	if !strings.Contains(out, "&amp; НДС") {
		t.Errorf("амперсанд не экранирован: %s", out)
	}
}

func TestRenderHTMLSetsCyrillicFont(t *testing.T) {
	if !strings.Contains(RenderHTML("x.pdf", "Проверка"), Font) {
		t.Error("шрифт с кириллицей не задан — получатель увидит квадраты")
	}
}

func TestRenderHTMLTurnsRuleIntoLine(t *testing.T) {
	out := RenderHTML("x.pdf", "Заголовок\n\n---\n\nТекст")

	if !strings.Contains(out, "<hr>") {
		t.Errorf("черта не отрисована: %s", out)
	}
	if strings.Contains(out, "<p>---</p>") {
		t.Error("«---» попало в документ текстом")
	}
}

func TestRenderHTMLEscapesTitle(t *testing.T) {
	out := RenderHTML("<b>x</b>", "Текст")
	if !strings.Contains(out, "<title>&lt;b&gt;x&lt;/b&gt;</title>") {
		t.Errorf("заголовок не экранирован: %s", out)
	}
}

func TestMarkdownToPDFRejectsEmpty(t *testing.T) {
	if _, err := MarkdownToPDF(context.Background(), "x", "   "); err == nil {
		t.Error("пустой документ должен быть ошибкой")
	}
}
