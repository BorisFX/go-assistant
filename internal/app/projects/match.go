package projects

import (
	"strings"
	"unicode"
)

// matchWindow is how far apart the words of a project key may sit and still
// count as one mention. "Ленина 42" must match "ул. Ленина, д. 42", but the
// word "Ленина" in one paragraph and the number 42 in another is a coincidence,
// not an address.
const matchWindow = 6

// MatchProject picks the project a message belongs to by looking for the project
// name or its address in the subject, body and sender. It returns the match and,
// when the text points at more than one project, the names of every candidate —
// an ambiguous letter is routed to the unsorted folder rather than guessed at,
// because a misfiled document is worse than an unfiled one.
func MatchProject(subject, body, from string, list []Project) (Project, []string) {
	haystack := textTokens(subject + " " + body + " " + from)

	var hits []Project
	for _, p := range list {
		if containsKey(haystack, p.Name) || containsKey(haystack, p.Address) {
			hits = append(hits, p)
		}
	}

	switch len(hits) {
	case 0:
		return Project{}, nil
	case 1:
		return hits[0], nil
	default:
		names := make([]string, 0, len(hits))
		for _, p := range hits {
			names = append(names, p.Name)
		}
		return Project{}, names
	}
}

// containsKey reports whether every word of the key appears in the text close
// together. Single characters are dropped: a stray "д" or "1" matches anything.
func containsKey(haystack []string, key string) bool {
	want := textTokens(key)
	if len(want) == 0 {
		return false
	}
	if len(want) == 1 {
		return indexOfToken(haystack, want[0], 0) >= 0
	}

	// Anchor on the first word, then require the rest inside the window.
	for at := 0; ; {
		start := indexOfToken(haystack, want[0], at)
		if start < 0 {
			return false
		}
		end := min(start+matchWindow, len(haystack))
		if allPresent(haystack[start:end], want[1:]) {
			return true
		}
		at = start + 1
	}
}

func indexOfToken(haystack []string, token string, from int) int {
	for i := from; i < len(haystack); i++ {
		if haystack[i] == token {
			return i
		}
	}
	return -1
}

func allPresent(window, want []string) bool {
	for _, w := range want {
		if indexOfToken(window, w, 0) < 0 {
			return false
		}
	}
	return true
}

// textTokens lowercases, folds ё to е and splits on everything that is not a
// letter or a digit, so "Ленина_42", "ЛЕНИНА, Д.42" and "ленина 42" all reduce
// to the same two words. Single-character tokens are dropped as noise.
func textTokens(s string) []string {
	var out []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 1 {
			out = append(out, word.String())
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			word.WriteRune('е')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}
