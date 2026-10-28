package legalreview

import (
	"context"
	"strings"
	"testing"
)

// A report cut off by the output limit loses its tail: in production the
// findings list stopped at the third of nineteen. Silence about that is the
// dangerous part — the reader cannot tell a short report from a truncated one.
func TestCoordinator_TruncatedReportIsMarked(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"ЗАМЕЧАНИЯ\n1. 🔴 Первое"}, finish: []string{"length"}}
	c := NewCoordinator(runner, "m", "r", "", 100000)

	got, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(got.Report, "ОБРЕЗАНО") {
		t.Errorf("truncation must be visible in the report:\n%s", got.Report)
	}
	if !strings.Contains(got.Report, "1. 🔴 Первое") {
		t.Error("the text produced so far must be kept")
	}
}

func TestCoordinator_CompleteReportIsNotMarked(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"ЗАМЕЧАНИЯ\n1. 🔴 Первое"}, finish: []string{"stop"}}
	c := NewCoordinator(runner, "m", "r", "", 100000)

	got, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if strings.Contains(got.Report, "ОБРЕЗАНО") {
		t.Errorf("a finished report must not be marked:\n%s", got.Report)
	}
}
