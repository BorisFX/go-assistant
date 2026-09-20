// Package docgen renders the assistant's markdown into print-ready documents.
// One renderer serves every outgoing paper — tender specs, review reports —
// so they all carry the same font, margins and table style.
package docgen

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// pdfTimeout bounds one conversion. LibreOffice warms up on the first run and
// then renders a document in a second or two.
const pdfTimeout = 3 * time.Minute

// Font is used for the whole document: Cyrillic in a default LibreOffice HTML
// import lands on a font that may not carry it, and the paper goes out as boxes.
const Font = "DejaVu Sans"

var (
	mdHeading = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	mdBullet  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	mdNumber  = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	mdBold    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// MarkdownToPDF renders markdown through LibreOffice and returns the PDF bytes.
func MarkdownToPDF(ctx context.Context, title, markdown string) ([]byte, error) {
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("pdf: нет текста документа")
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
	if err := os.WriteFile(htmlPath, []byte(RenderHTML(title, markdown)), 0o644); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, soffice, "--headless", "--norestore", "--invisible",
		// A separate profile: parallel conversions otherwise fight over the
		// shared one and the second silently does nothing.
		"-env:UserInstallation=file://"+filepath.Join(workdir, "profile"),
		"--convert-to", "pdf", "--outdir", workdir, htmlPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdf: LibreOffice не собрал документ: %s", strings.TrimSpace(string(out)))
	}

	data, err := os.ReadFile(filepath.Join(workdir, "doc.pdf"))
	if err != nil {
		return nil, fmt.Errorf("pdf: результат не найден: %w", err)
	}
	return data, nil
}

// RenderHTML turns markdown into print-ready HTML. A full markdown engine is
// not worth pulling in: our papers are headings, lists, tables and rules.
func RenderHTML(title, content string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\">")
	fmt.Fprintf(&b, "<title>%s</title><style>", html.EscapeString(title))
	fmt.Fprintf(&b, "body{font-family:'%s',sans-serif;font-size:11pt;line-height:1.4;margin:2cm}", Font)
	b.WriteString("h1{font-size:16pt}h2{font-size:13pt}h3{font-size:12pt}")
	b.WriteString("table{border-collapse:collapse;width:100%}td,th{border:1px solid #666;padding:4px;font-size:10pt}")
	b.WriteString("</style></head><body>")

	inList, inTable := false, false
	var tableRows [][]string
	closeBlocks := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
		if inTable {
			writeTable(&b, tableRows)
			tableRows = nil
			inTable = false
		}
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			closeBlocks()
		case isRule(trimmed):
			// A markdown horizontal rule: otherwise "---" lands in the paper
			// as a stray line of text.
			closeBlocks()
			b.WriteString("<hr>")
		case strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|"):
			cells := splitRow(trimmed)
			if isTableDivider(cells) {
				continue
			}
			if !inTable {
				closeBlocks()
				inTable = true
			}
			tableRows = append(tableRows, cells)
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

// writeTable emits a buffered table with explicit column widths. LibreOffice
// ignores CSS widths on HTML import and squeezes every column to one
// character, so widths go as attributes, proportional to the longest cell.
func writeTable(b *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	total := 0
	for i := 0; i < cols; i++ {
		longest := 0
		for _, r := range rows {
			if i < len(r) && len([]rune(r[i])) > longest {
				longest = len([]rune(r[i]))
			}
		}
		// Floor keeps a "№" column readable, cap stops one long cell from
		// starving the rest.
		if longest < 4 {
			longest = 4
		}
		if longest > 40 {
			longest = 40
		}
		widths[i] = longest
		total += longest
	}
	b.WriteString(`<table border="1" cellspacing="0" cellpadding="4" width="100%">`)
	for ri, r := range rows {
		b.WriteString("<tr>")
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(r) {
				cell = inline(r[i])
			}
			tag := "td"
			if ri == 0 {
				tag = "th"
			}
			fmt.Fprintf(b, `<%s width="%d%%">%s</%s>`, tag, widths[i]*100/total, cell, tag)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
}
