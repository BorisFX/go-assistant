package norms

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestCorpusReport is an operator check, not a unit test: point NORMS_CHECK_DIR
// at a corpus folder and it prints how each document was chunked and whether
// the references the pipeline relies on resolve. Skipped in normal runs.
func TestCorpusReport(t *testing.T) {
	dir := os.Getenv("NORMS_CHECK_DIR")
	if dir == "" {
		t.Skip("NORMS_CHECK_DIR not set")
	}
	sources, err := LoadCorpus(context.Background(), dir)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(sources) == 0 {
		t.Fatalf("no documents in %s", dir)
	}

	// References a suspension review typically needs to resolve exactly.
	probes := map[string][]string{
		"218-ФЗ":           {"ст. 26", "ст. 26 ч. 1 п. 7", "ст. 14", "ст. 18", "ст. 21", "ст. 27", "ст. 29"},
		"ГрК РФ":           {"ст. 49", "ст. 51", "ст. 51 ч. 7", "ст. 55", "ст. 57.3"},
		"ЗК РФ":            {"ст. 7", "ст. 39.6"},
		"СП 4.13130.2013":  {"п. 4.3"},
		"СП 42.13330.2016": {"п. 7.1"},
	}

	for _, src := range sources {
		chunks := Split(src.Text)
		refs := map[string]int{}
		for _, c := range chunks {
			refs[NormalizeRef(c.Ref)]++
		}
		t.Logf("%-28s chars=%-8d chunks=%-5d distinct_refs=%d", src.Document.Code, len(src.Text), len(chunks), len(refs))
		if len(chunks) < 5 {
			t.Errorf("%s: only %d chunks — extractor or chunker failed", src.Document.Code, len(chunks))
		}
		want, ok := probes[src.Document.Code]
		if !ok {
			continue
		}
		var missing []string
		for _, w := range want {
			if !hasRefPrefix(refs, NormalizeRef(w)) {
				missing = append(missing, w)
			}
		}
		if len(missing) > 0 {
			sample := sampleRefs(refs, 8)
			t.Errorf("%s: refs not found: %v (sample of what exists: %v)", src.Document.Code, missing, sample)
		}
	}
}

func hasRefPrefix(refs map[string]int, prefix string) bool {
	for r := range refs {
		if r == prefix || strings.HasPrefix(r, prefix+" ") {
			return true
		}
	}
	return false
}

func sampleRefs(refs map[string]int, n int) []string {
	out := make([]string, 0, len(refs))
	for r := range refs {
		out = append(out, r)
	}
	sort.Strings(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}
