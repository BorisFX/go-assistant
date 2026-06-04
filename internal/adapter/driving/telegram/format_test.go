package telegram

import (
	"strings"
	"testing"
)

func TestFormatForTelegramConvertsTable(t *testing.T) {
	in := strings.Join([]string{
		"## Анализ",
		"",
		"| № | Замечание | Статус |",
		"|----|-----------|--------|",
		"| 1 | Подписано ненадлежащим лицом | КРИТИЧНО |",
		"| 2 | Не заполнен раздел | ОК |",
		"",
		"---",
		"",
		"Готово.",
	}, "\n")

	got := formatForTelegram(in)

	// No raw Markdown artifacts should survive.
	for _, bad := range []string{"|", "##", "---", "**"} {
		if strings.Contains(got, bad) {
			t.Errorf("output still contains %q:\n%s", bad, got)
		}
	}
	// Heading text is kept, just unmarked.
	if !strings.Contains(got, "Анализ") {
		t.Errorf("heading text lost:\n%s", got)
	}
	// Table rows become key: value bullets keyed by the header.
	if !strings.Contains(got, "1. Замечание: Подписано ненадлежащим лицом") {
		t.Errorf("row 1 not rendered as expected:\n%s", got)
	}
	if !strings.Contains(got, "Статус: КРИТИЧНО") {
		t.Errorf("status cell missing:\n%s", got)
	}
	if !strings.Contains(got, "Готово.") {
		t.Errorf("trailing prose lost:\n%s", got)
	}
}

func TestFormatForTelegramStripsBold(t *testing.T) {
	got := formatForTelegram("Это **важно** и __тоже__.")
	if got != "Это важно и тоже." {
		t.Errorf("bold not stripped: %q", got)
	}
}

func TestFormatForTelegramLeavesPlainText(t *testing.T) {
	in := "Просто текст.\nВторая строка с одним | символом."
	got := formatForTelegram(in)
	if got != in {
		t.Errorf("plain text altered:\nwant %q\n got %q", in, got)
	}
}

func TestIsTableSeparator(t *testing.T) {
	if !isTableSeparator([]string{"----", ":--:", "---"}) {
		t.Error("divider row not detected")
	}
	if isTableSeparator([]string{"1", "текст"}) {
		t.Error("data row wrongly treated as divider")
	}
}
