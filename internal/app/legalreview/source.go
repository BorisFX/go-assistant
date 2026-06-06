package legalreview

import (
	"path/filepath"
	"regexp"
	"strings"
)

var reviewFolderRe = regexp.MustCompile(`(?i)^\s*разбери\s+папку\s+(.+?)\s*$`)

// parseReviewFolder extracts the folder name/path from the "разбери папку X" intent.
func parseReviewFolder(text string) (string, bool) {
	m := reviewFolderRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	folder := strings.Trim(strings.TrimSpace(m[1]), `"'«»`)
	if folder == "" {
		return "", false
	}
	return folder, true
}

// filterByExt keeps paths with an allowed extension (case-insensitive),
// preserving the original order.
func filterByExt(paths, exts []string) []string {
	allow := make(map[string]bool, len(exts))
	for _, e := range exts {
		allow[strings.ToLower(e)] = true
	}
	var out []string
	for _, p := range paths {
		if allow[strings.ToLower(filepath.Ext(p))] {
			out = append(out, p)
		}
	}
	return out
}

// reviewExtensions — document types we review (design: PDF + office).
var reviewExtensions = []string{".pdf", ".doc", ".docx", ".xls", ".xlsx"}
