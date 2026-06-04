package telegram

import (
	"regexp"
	"strings"
)

// formatForTelegram converts the GitHub-flavored Markdown that LLMs love to emit
// (tables, ## headings, ---, **bold**) into plain text that reads correctly in
// Telegram. Telegram's legacy Markdown parser does not support tables at all and
// chokes on unbalanced markers, so the bot sends this output as plain text.
func formatForTelegram(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	var table [][]string
	flush := func() {
		if len(table) > 0 {
			out = append(out, renderTableRows(table)...)
			table = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if isTableRow(trimmed) {
			cells := splitTableRow(trimmed)
			if !isTableSeparator(cells) {
				table = append(table, cells)
			}
			continue
		}
		flush()

		if isHorizontalRule(trimmed) {
			continue
		}
		out = append(out, stripInlineMarkdown(stripHeading(line)))
	}
	flush()

	return strings.Join(out, "\n")
}

var (
	reHeading   = regexp.MustCompile(`^\s*#{1,6}\s*`)
	reBoldStar  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUnder = regexp.MustCompile(`__(.+?)__`)
	reNumeric   = regexp.MustCompile(`^\d+$`)
)

func stripHeading(line string) string {
	return reHeading.ReplaceAllString(line, "")
}

// stripInlineMarkdown removes emphasis markers that would otherwise show up as
// literal characters once the message is sent as plain text.
func stripInlineMarkdown(s string) string {
	s = reBoldStar.ReplaceAllString(s, "$1")
	s = reBoldUnder.ReplaceAllString(s, "$1")
	return s
}

func isHorizontalRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	for _, r := range trimmed {
		if r != '-' && r != '*' && r != '_' {
			return false
		}
	}
	return true
}

// isTableRow reports whether a line looks like a Markdown table row: it must
// carry at least two column separators so ordinary prose with a stray pipe is
// not mistaken for a table.
func isTableRow(trimmed string) bool {
	return strings.Count(trimmed, "|") >= 2
}

func splitTableRow(trimmed string) []string {
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// isTableSeparator matches the |---|:--:| divider row between a table header and
// its body.
func isTableSeparator(cells []string) bool {
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return len(cells) > 0
}

// renderTableRows turns parsed table rows into a bulleted "key: value" block.
// The first row is treated as the header supplying labels for each column.
func renderTableRows(rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	header := rows[0]

	var out []string
	for _, row := range rows[1:] {
		var parts []string
		for i, cell := range row {
			cell = strings.TrimSpace(cell)
			if cell == "" {
				continue
			}
			key := ""
			if i < len(header) {
				key = strings.TrimSpace(header[i])
			}
			if key == "" || key == "#" || key == "№" {
				parts = append(parts, cell)
			} else {
				parts = append(parts, key+": "+cell)
			}
		}
		if len(parts) == 0 {
			continue
		}

		lead := parts[0]
		rest := parts[1:]
		bullet := "•"
		// Fold a leading row number ("1", "2", ...) into the bullet itself.
		if reNumeric.MatchString(lead) && len(rest) > 0 {
			bullet = lead + "."
			lead = rest[0]
			rest = rest[1:]
		}
		out = append(out, bullet+" "+lead)
		for _, p := range rest {
			out = append(out, "   "+p)
		}
	}
	return out
}
