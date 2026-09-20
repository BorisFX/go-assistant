package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// pdfTimeout bounds one conversion. LibreOffice warms up on the first run and
// then renders a tech spec in a second or two.
const pdfTimeout = 3 * time.Minute

// pdfFont is used for the whole document: Cyrillic in a default LibreOffice
// HTML import lands on a font that may not carry it, and the spec goes out as
// boxes to a contractor.
const pdfFont = "DejaVu Sans"

// createPDF renders text into a PDF, puts it in Drive and keeps a local copy —
// the copy is what can then be attached to a tender letter. Contractors expect
// a document, not a .txt in the body.
func (d *DriveFiles) createPDF(ctx context.Context, path, name, content string) (json.RawMessage, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("pdf: нет текста документа")
	}
	if name == "" {
		return nil, fmt.Errorf("pdf: не указано имя файла")
	}
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		name += ".pdf"
	}

	soffice, err := exec.LookPath("soffice")
	if err != nil {
		return nil, fmt.Errorf("pdf: на сервере нет LibreOffice, документ не собрать")
	}

	workdir, err := os.MkdirTemp("", "pdf")
	if err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	defer os.RemoveAll(workdir)

	htmlPath := filepath.Join(workdir, "doc.html")
	if err := os.WriteFile(htmlPath, []byte(renderHTML(name, content)), 0o644); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, soffice, "--headless", "--norestore", "--invisible",
		// Отдельный профиль: параллельные конвертации иначе дерутся за общий,
		// и вторая молча не делает ничего.
		"-env:UserInstallation=file://"+filepath.Join(workdir, "profile"),
		"--convert-to", "pdf", "--outdir", workdir, htmlPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdf: LibreOffice не собрал документ: %s", strings.TrimSpace(string(out)))
	}

	data, err := os.ReadFile(filepath.Join(workdir, "doc.pdf"))
	if err != nil {
		return nil, fmt.Errorf("pdf: результат не найден: %w", err)
	}

	// Локальная копия живёт в рабочей папке бота — только оттуда письмо может
	// взять вложение.
	localDir := filepath.Join(d.filesDir, "pdf")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	localPath := filepath.Join(localDir, sanitizeFileName(name))
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}

	result := map[string]any{
		"name":       name,
		"local_path": localPath,
		"size":       len(data),
		"hint":       "для вложения в письмо используйте local_path",
	}

	folderID, err := d.client.EnsurePath(ctx, path)
	if err != nil {
		// Файл уже собран: сообщаем про Drive, но путь для вложения отдаём.
		slog.Warn("pdf: не удалось открыть папку на Диске", "path", path, "error", err)
		result["drive_error"] = err.Error()
		return json.Marshal(result)
	}
	info, err := d.client.Upload(ctx, folderID, name, "application/pdf", data)
	if err != nil {
		slog.Warn("pdf: не удалось загрузить на Диск", "path", path, "error", err)
		result["drive_error"] = err.Error()
		return json.Marshal(result)
	}
	result["file_id"] = info.ID
	result["drive_path"] = strings.Trim(path+"/"+name, "/")
	return json.Marshal(result)
}

var (
	mdHeading = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	mdBullet  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	mdNumber  = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	mdBold    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// renderHTML turns the model's markdown into print-ready HTML. A full markdown
// engine is not worth pulling in: a tech spec is headings, lists and tables.
func renderHTML(title, content string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>")
	fmt.Fprintf(&b, "body{font-family:'%s',sans-serif;font-size:11pt;line-height:1.4;margin:2cm}", pdfFont)
	b.WriteString("h1{font-size:16pt}h2{font-size:13pt}h3{font-size:12pt}")
	b.WriteString("table{border-collapse:collapse;width:100%}td,th{border:1px solid #666;padding:4px;font-size:10pt}")
	b.WriteString("</style></head><body>")

	inList, inTable := false, false
	closeBlocks := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
		if inTable {
			b.WriteString("</table>")
			inTable = false
		}
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			closeBlocks()
		case isRule(trimmed):
			// Горизонтальная черта markdown: иначе «---» уезжает в документ
			// отдельной строкой посреди технического задания.
			closeBlocks()
			b.WriteString("<hr>")
		case strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|"):
			cells := splitRow(trimmed)
			if isTableDivider(cells) {
				continue
			}
			if !inTable {
				closeBlocks()
				b.WriteString("<table>")
				inTable = true
			}
			b.WriteString("<tr>")
			for _, c := range cells {
				fmt.Fprintf(&b, "<td>%s</td>", inline(c))
			}
			b.WriteString("</tr>")
		case mdHeading.MatchString(trimmed):
			closeBlocks()
			m := mdHeading.FindStringSubmatch(trimmed)
			level := len(m[1])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, inline(m[2]), level)
		case mdBullet.MatchString(trimmed):
			if !inList {
				closeBlocks()
				b.WriteString("<ul>")
				inList = true
			}
			fmt.Fprintf(&b, "<li>%s</li>", inline(mdBullet.FindStringSubmatch(trimmed)[1]))
		case mdNumber.MatchString(trimmed):
			m := mdNumber.FindStringSubmatch(trimmed)
			closeBlocks()
			fmt.Fprintf(&b, "<p><b>%s.</b> %s</p>", m[1], inline(m[2]))
		default:
			closeBlocks()
			fmt.Fprintf(&b, "<p>%s</p>", inline(trimmed))
		}
	}
	closeBlocks()
	b.WriteString("</body></html>")
	return b.String()
}

func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isTableDivider(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

func inline(s string) string {
	escaped := html.EscapeString(s)
	return mdBold.ReplaceAllString(escaped, "<b>$1</b>")
}

// isRule reports a markdown horizontal rule (---, ***, ___).
func isRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, r := range []string{"-", "*", "_"} {
		if strings.Trim(line, r) == "" {
			return true
		}
	}
	return false
}
