package builtin

import (
	"strings"
	"testing"
)

// The spec goes to a stranger, so structure has to survive: headings, lists and
// the table of works, not one flat blob of text.
func TestRenderHTMLKeepsStructure(t *testing.T) {
	out := renderHTML("ТЗ.pdf", `# Техническое задание

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
	// Разделитель markdown-таблицы не должен превратиться в строку документа.
	if strings.Contains(out, "<td>---</td>") {
		t.Error("разделитель таблицы попал в документ")
	}
}

// Текст подрядчика — не разметка: угловые скобки не должны ломать документ.
func TestRenderHTMLEscapesText(t *testing.T) {
	out := renderHTML("x.pdf", "Смета <не менее> 100 000 ₽ & НДС")

	if strings.Contains(out, "<не менее>") {
		t.Errorf("текст не экранирован: %s", out)
	}
	if !strings.Contains(out, "&amp; НДС") {
		t.Errorf("амперсанд не экранирован: %s", out)
	}
}

func TestRenderHTMLSetsCyrillicFont(t *testing.T) {
	if !strings.Contains(renderHTML("x.pdf", "Проверка"), pdfFont) {
		t.Error("шрифт с кириллицей не задан — подрядчик получит квадраты")
	}
}

func TestRenderHTMLTurnsRuleIntoLine(t *testing.T) {
	out := renderHTML("x.pdf", "Заголовок\n\n---\n\nТекст")

	if !strings.Contains(out, "<hr>") {
		t.Errorf("черта не отрисована: %s", out)
	}
	if strings.Contains(out, "<p>---</p>") {
		t.Error("«---» попало в документ текстом")
	}
}
