package legalreview

import (
	"path/filepath"
	"regexp"
	"strings"
)

var reviewFolderRe = regexp.MustCompile(`(?i)^\s*разбери\s+папку\s+(.+?)\s*$`)

// focusSeparators split the folder from an optional task after it:
// «разбери папку X: замечания Росреестра», «разбери папку X — что с этажностью».
var focusSeparators = []string{" — ", " – ", " - ", ": ", ":"}

// ParseReviewFolder extracts the folder name/path from the "разбери папку X" intent.
func ParseReviewFolder(text string) (string, bool) {
	folder, _, ok := ParseReviewIntent(text)
	return folder, ok
}

// ParseReviewIntent extracts the folder and an optional focus (the question the
// user wants answered) from the "разбери папку X[: focus]" intent. A folder
// path may itself contain ':' only in the Windows-drive sense, which never
// appears here, so the first separator wins.
func ParseReviewIntent(text string) (folder, focus string, ok bool) {
	m := reviewFolderRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	rest := strings.TrimSpace(m[1])
	for _, sep := range focusSeparators {
		if i := strings.Index(rest, sep); i > 0 {
			folder, focus = rest[:i], strings.TrimSpace(rest[i+len(sep):])
			break
		}
	}
	if folder == "" {
		folder = rest
	}
	folder = strings.Trim(strings.TrimSpace(folder), `"'«»`)
	if folder == "" {
		return "", "", false
	}
	return folder, focus, true
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

// ReviewExtensions — document types we review (design: PDF + office + XML tech
// plans + drawings + detached signatures). XML is read directly as text by the
// extraction Router; .sig files are rendered as "who signed" pages.
var ReviewExtensions = []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".xml", ".txt", ".dwg", ".dxf", ".sig"}

// RE2 \b is ASCII-only, so the word end is spelled out for Cyrillic.
var reviewCaptionRe = regexp.MustCompile(`(?i)^\s*(разбери|проверь)(?:$|[\s:—–-]+)(.*)$`)

// ParseReviewCaption recognises the "разбери …" intent on a file sent to the
// bot: the whole caption after the verb is the focus of the review. This is
// the lead-magnet path — a counterparty's PDF forwarded with one word.
func ParseReviewCaption(caption string) (focus string, ok bool) {
	m := reviewCaptionRe.FindStringSubmatch(caption)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[2]), true
}
