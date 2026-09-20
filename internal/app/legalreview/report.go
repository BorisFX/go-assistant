package legalreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FileInfo describes one reviewed document as it went into the batch. The
// hash pins the report to the exact bytes: a client can re-check that the
// conclusion was drawn from the files they sent, not a later revision.
type FileInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Method string `json:"method,omitempty"` // extraction provenance, when known
	Bytes  int64  `json:"bytes"`
	Read   bool   `json:"read"`
}

// Run is one complete review: what was checked, with what, and the result.
// It is the unit of persistence and the source of the client report.
type Run struct {
	ID     uuid.UUID
	Folder string
	Focus  string
	Files  []FileInfo
	Report string // coordinator output
	Models struct {
		Digest      string
		Coordinator string
	}
	NormativyHash string
	PDFPath       string
	StartedAt     time.Time
	FinishedAt    time.Time
}

// RunStore persists finished runs. Declared here, next to the consumer, so the
// package is testable without Postgres.
type RunStore interface {
	Save(ctx context.Context, run *Run) (uuid.UUID, error)
	ListRecent(ctx context.Context, limit int) ([]Run, error)
}

// DescribeFiles stats and hashes the collected paths. A file that cannot be
// read is still listed (Read=false) so the report shows it was in the batch.
func DescribeFiles(paths []string) []FileInfo {
	out := make([]FileInfo, 0, len(paths))
	for _, p := range paths {
		fi := FileInfo{Path: p, Name: filepath.Base(p)}
		if st, err := os.Stat(p); err == nil {
			fi.Bytes = st.Size()
		}
		if sum, err := hashFile(p); err == nil {
			fi.SHA256 = sum
			fi.Read = true
		}
		out = append(out, fi)
	}
	return out
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashText fingerprints the normative base that framed the coordinator, so a
// report can be traced to the exact rules it was checked against.
func HashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ReportFileName is the PDF name for a run: the folder plus a timestamp, so
// repeated reviews of one object do not overwrite each other.
func ReportFileName(run Run) string {
	when := run.FinishedAt
	if when.IsZero() {
		when = time.Now()
	}
	return fmt.Sprintf("Заключение_%s_%s.pdf", safeName(run.Folder), when.Format("2006-01-02_1504"))
}

func safeName(s string) string {
	repl := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_", "«", "", "»", "", "\"", "")
	s = strings.Trim(repl.Replace(strings.TrimSpace(s)), "_")
	if s == "" {
		return "документы"
	}
	return s
}

const limitationsText = `- Проверка выполнена автоматизированной системой на основании текста представленных документов; она не заменяет заключение эксперта и требует подтверждения ответственным специалистом.
- Электронные подписи (.sig) прочитаны в части сведений о подписантах; криптографическая проверка подписи, срока действия и цепочки сертификатов не выполнялась.
- Ссылки на нормативные акты приводятся только по загруженной нормативной базе; пункты, отсутствующие в базе, помечены как требующие проверки по актуальной редакции.
- Документы со статусом «не прочитан» в оценке не участвовали: вывод по ним невозможен.
- Данные, извлечённые из сканов и чертежей средствами распознавания, могут содержать ошибки распознавания и подлежат сверке с оригиналом.`

// BuildReportMarkdown renders a run as the client-facing paper. Everything
// numeric comes from the run itself; the model's text is carried verbatim in
// its own section so a reader can tell facts from findings.
func BuildReportMarkdown(run Run) string {
	var b strings.Builder
	b.WriteString("# ЗАКЛЮЧЕНИЕ ПО РЕЗУЛЬТАТАМ АВТОМАТИЗИРОВАННОЙ ПРОВЕРКИ ДОКУМЕНТАЦИИ\n\n")

	fmt.Fprintf(&b, "**Объект / папка:** %s\n\n", run.Folder)
	if strings.TrimSpace(run.Focus) != "" {
		fmt.Fprintf(&b, "**Предмет проверки:** %s\n\n", run.Focus)
	}
	when := run.FinishedAt
	if when.IsZero() {
		when = time.Now()
	}
	fmt.Fprintf(&b, "**Дата:** %s\n\n", when.Format("02.01.2006"))
	if run.ID != uuid.Nil {
		fmt.Fprintf(&b, "**Идентификатор проверки:** %s\n\n", run.ID)
	}

	b.WriteString("## Состав проверенных документов\n\n")
	b.WriteString("| № | Файл | Размер | Способ чтения | Статус | SHA-256 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	read, unread := 0, 0
	for i, f := range run.Files {
		status := "прочитан"
		if !f.Read {
			status = "не прочитан"
			unread++
		} else {
			read++
		}
		method := f.Method
		if method == "" {
			method = "—"
		}
		sum := f.SHA256
		if len(sum) > 12 {
			sum = sum[:12]
		}
		if sum == "" {
			sum = "—"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n",
			i+1, f.Name, formatBytes(f.Bytes), method, status, sum)
	}
	fmt.Fprintf(&b, "\nВсего документов: %d, прочитано: %d, не прочитано: %d.\n\n", len(run.Files), read, unread)

	b.WriteString("## РЕЗУЛЬТАТЫ ПРОВЕРКИ\n\n")
	b.WriteString(strings.TrimSpace(run.Report))
	b.WriteString("\n\n")

	b.WriteString("## ОГРАНИЧЕНИЯ\n\n")
	b.WriteString(limitationsText)
	b.WriteString("\n\n")

	b.WriteString("## Сведения о проверке\n\n")
	if run.Models.Digest != "" || run.Models.Coordinator != "" {
		fmt.Fprintf(&b, "- Модели: анализ документов — %s; сводное заключение — %s\n",
			orDash(run.Models.Digest), orDash(run.Models.Coordinator))
	}
	if run.NormativyHash != "" {
		fmt.Fprintf(&b, "- Версия нормативной базы: %s\n", run.NormativyHash[:min(12, len(run.NormativyHash))])
	}
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		fmt.Fprintf(&b, "- Время проверки: %s\n", run.FinishedAt.Sub(run.StartedAt).Round(time.Second))
	}
	b.WriteString("\n---\n\n")
	b.WriteString("**Проверил и подтверждаю:**\n\n")
	b.WriteString("Технический заказчик: ____________________\n\n")
	b.WriteString("Дата: ____________\n")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f КБ", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d Б", n)
	}
}
