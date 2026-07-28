package projects

import "strings"

// StageStatus is the comparison between what a stage should produce and what is
// actually in its folder.
type StageStatus struct {
	Num      int      `json:"num"`
	Stage    string   `json:"stage"`
	Folder   string   `json:"folder"`
	Files    []string `json:"files"`
	Present  []string `json:"present"`
	Missing  []string `json:"missing"`
	Agencies []string `json:"agencies,omitempty"`
}

// Done reports whether every expected document has a matching file.
func (s StageStatus) Done() bool { return len(s.Missing) == 0 && len(s.Present) > 0 }

// normalize folds a string for comparison: lowercase, ё as е, punctuation and
// digits dropped. Document names in practice differ by case, by ё, and by the
// numbering people add, and none of that should defeat a match.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			b.WriteRune('е')
		case r >= 'а' && r <= 'я', r >= 'a' && r <= 'z':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// minStem is how much of a word must coincide to call it the same word.
// Shorter prefixes start matching unrelated Russian words through their common
// roots, longer ones miss ordinary declension.
const minStem = 5

func tokens(s string) []string {
	var out []string
	for _, w := range strings.Fields(normalize(s)) {
		if len([]rune(w)) >= minStem {
			out = append(out, w)
		}
	}
	return out
}

// matches reports whether a file name plausibly is the expected document.
// Comparison is by word stem, so "Технические планы" matches "Техплан №3.pdf"
// only if they share a long enough beginning — this is a hint for the model,
// not a legal conclusion, and the raw file list travels alongside it.
func matches(expected, fileName string) bool {
	fileWords := tokens(fileName)
	for _, e := range tokens(expected) {
		for _, f := range fileWords {
			if sharedPrefix(e, f) >= minStem {
				return true
			}
		}
	}
	return false
}

func sharedPrefix(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[n] == br[n] {
		n++
	}
	return n
}

// CheckStage compares a stage's expected documents against the files found in
// its folder. Both lists are reported so the caller can see the evidence rather
// than trust the verdict.
func CheckStage(stage Stage, files []string) StageStatus {
	st := StageStatus{
		Num:      stage.Num,
		Stage:    stage.Name,
		Folder:   stage.Folder(),
		Files:    files,
		Agencies: stage.Agencies,
	}
	for _, exp := range stage.Expected {
		found := false
		for _, f := range files {
			if matches(exp, f) {
				found = true
				break
			}
		}
		if found {
			st.Present = append(st.Present, exp)
		} else {
			st.Missing = append(st.Missing, exp)
		}
	}
	return st
}

// CurrentStage is the first stage that is not finished. A project whose every
// stage is complete returns the last one, so callers always have something to
// report.
func CurrentStage(statuses []StageStatus) StageStatus {
	for _, s := range statuses {
		if !s.Done() {
			return s
		}
	}
	if len(statuses) > 0 {
		return statuses[len(statuses)-1]
	}
	return StageStatus{}
}
