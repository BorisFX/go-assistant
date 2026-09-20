package legalreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDescribeFilesHashesAndMarksUnreadable(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "техплан.pdf")
	if err := os.WriteFile(good, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "нет.pdf")

	files := DescribeFiles([]string{good, missing})
	if len(files) != 2 {
		t.Fatalf("описано %d файлов, ждали 2", len(files))
	}
	if !files[0].Read || files[0].Bytes != 5 || files[0].Name != "техплан.pdf" {
		t.Errorf("первый файл: %+v", files[0])
	}
	// sha256("hello")
	if files[0].SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("хэш: %s", files[0].SHA256)
	}
	if files[1].Read || files[1].SHA256 != "" {
		t.Errorf("пропавший файл должен быть непрочитанным: %+v", files[1])
	}
}

func TestBuildReportMarkdownHasEverySection(t *testing.T) {
	run := Run{
		Folder: "Vertex/03_Техпланы",
		Focus:  "замечания Росреестра",
		Files: []FileInfo{
			{Name: "техплан.pdf", SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e", Bytes: 2048, Method: "pdftotext", Read: true},
			{Name: "скан.pdf", Bytes: 5 << 20, Read: false},
		},
		Report:        "1. 🔴 Площадь: 11 915,4 vs 11 939,6\n   Документы: техплан стр. 4",
		NormativyHash: "abcdef1234567890",
		StartedAt:     time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 9, 20, 10, 3, 0, 0, time.UTC),
	}
	run.Models.Digest = "deepseek/deepseek-v4-flash"
	run.Models.Coordinator = "anthropic/claude-sonnet-4.6"

	md := BuildReportMarkdown(run)
	for _, want := range []string{
		"# ЗАКЛЮЧЕНИЕ ПО РЕЗУЛЬТАТАМ АВТОМАТИЗИРОВАННОЙ ПРОВЕРКИ ДОКУМЕНТАЦИИ",
		"**Объект / папка:** Vertex/03_Техпланы",
		"**Предмет проверки:** замечания Росреестра",
		"**Дата:** 20.09.2026",
		"| 1 | техплан.pdf | 2 КБ | pdftotext | прочитан | 2cf24dba5fb0 |",
		"| 2 | скан.pdf | 5.0 МБ | — | не прочитан | — |",
		"Всего документов: 2, прочитано: 1, не прочитано: 1.",
		"## РЕЗУЛЬТАТЫ ПРОВЕРКИ",
		"11 915,4 vs 11 939,6",
		"## ОГРАНИЧЕНИЯ",
		"криптографическая проверка",
		"anthropic/claude-sonnet-4.6",
		"Версия нормативной базы: abcdef123456",
		"Время проверки: 3m0s",
		"Технический заказчик: ____________________",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("в отчёте нет %q\n%s", want, md)
		}
	}
}

func TestBuildReportMarkdownWithoutOptionalFields(t *testing.T) {
	md := BuildReportMarkdown(Run{Folder: "X", Report: "Нарушений не выявлено."})
	if strings.Contains(md, "Предмет проверки") || strings.Contains(md, "Идентификатор") {
		t.Error("пустые поля не должны печататься")
	}
	if !strings.Contains(md, "Нарушений не выявлено.") {
		t.Error("текст координатора потерян")
	}
}

func TestReportFileNameIsSafe(t *testing.T) {
	run := Run{Folder: "/НОГИНСК ОБЪЕКТЫ/СОЛОЩУК: 11 915м2/", FinishedAt: time.Date(2026, 9, 20, 14, 5, 0, 0, time.UTC)}
	got := ReportFileName(run)
	if got != "Заключение_НОГИНСК_ОБЪЕКТЫ_СОЛОЩУК__11_915м2_2026-09-20_1405.pdf" {
		t.Errorf("имя файла: %q", got)
	}
	if strings.ContainsAny(got, "/\\:") {
		t.Errorf("в имени файла остались опасные символы: %q", got)
	}
}

func TestHashTextIsStable(t *testing.T) {
	if HashText("a") != HashText("a") || HashText("a") == HashText("b") {
		t.Error("хэш нормативки нестабилен")
	}
}
