package legalreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Runs the verifier over a real production run: set REVIEW_REPORT to the
// report markdown and REVIEW_CACHE to the digest-cache directory.
func TestVerifyAgainstRealRun(t *testing.T) {
	reportPath, cacheDir := os.Getenv("REVIEW_REPORT"), os.Getenv("REVIEW_CACHE")
	if reportPath == "" || cacheDir == "" {
		t.Skip("set REVIEW_REPORT and REVIEW_CACHE")
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no cached digests: %v", err)
	}
	var evidence []string
	for _, e := range entries {
		data, err := os.ReadFile(e)
		if err != nil {
			t.Fatal(err)
		}
		var d Digest
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, d.Text)
		if f, err := json.Marshal(d.Facts); err == nil {
			evidence = append(evidence, string(f))
		}
	}
	for _, u := range Verify(string(report), evidence) {
		t.Logf("UNVERIFIED %-20s %s  «%s»", u.Value, u.Kind, u.Quote)
	}
}
