package legalreview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/extraction"
	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func TestChunkPages_SingleBatchWhenUnderBudget(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: "aaaa"},
		{Number: 2, Text: "bbbb"},
	}
	batches := chunkPages(pages, 1000)
	if len(batches) != 1 {
		t.Fatalf("want 1 batch, got %d", len(batches))
	}
	if len(batches[0]) != 2 {
		t.Fatalf("want 2 pages in batch, got %d", len(batches[0]))
	}
}

func TestChunkPages_SplitsWhenOverBudget(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: strings.Repeat("a", 6)},
		{Number: 2, Text: strings.Repeat("b", 6)},
		{Number: 3, Text: strings.Repeat("c", 6)},
	}
	// Budget 10 chars: each page is 6, so two pages (12) overflow -> 1 page per batch.
	batches := chunkPages(pages, 10)
	if len(batches) != 3 {
		t.Fatalf("want 3 batches, got %d", len(batches))
	}
}

func TestChunkPages_OversizedSinglePageGetsOwnBatch(t *testing.T) {
	pages := []output.PDFPage{
		{Number: 1, Text: strings.Repeat("x", 50)},
		{Number: 2, Text: "y"},
	}
	batches := chunkPages(pages, 10)
	if len(batches) != 2 {
		t.Fatalf("want 2 batches, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].Number != 1 {
		t.Fatalf("oversized page must be alone in its batch: %+v", batches[0])
	}
}

func TestChunkPages_EmptyIsNil(t *testing.T) {
	if b := chunkPages(nil, 100); b != nil {
		t.Fatalf("want nil for no pages, got %+v", b)
	}
}

// fakeRunner records each Run call and returns scripted output per call.
type fakeRunner struct {
	outputs []string
	err     error
	errs    []error  // per-call errors, used before err
	finish  []string // per-call finish reasons reported through the hook
	tasks   []string
	cfgs    []subagent.Config
}

func (f *fakeRunner) Run(_ context.Context, cfg subagent.Config, task string) (string, error) {
	f.tasks = append(f.tasks, task)
	f.cfgs = append(f.cfgs, cfg)
	if n := len(f.tasks) - 1; n < len(f.finish) && cfg.OnFinishReason != nil {
		cfg.OnFinishReason(f.finish[n])
	}
	if n := len(f.tasks) - 1; n < len(f.errs) && f.errs[n] != nil {
		return "", f.errs[n]
	}
	if f.err != nil {
		return "", f.err
	}
	out := "digest"
	if len(f.tasks)-1 < len(f.outputs) {
		out = f.outputs[len(f.tasks)-1]
	}
	return out, nil
}

func TestDigest_SingleBatchCitesPathAndPages(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"РЕЗЮМЕ: договор. Факт (стр. 1)"}}
	w := NewDigestWorker(runner, "deepseek/deepseek-v4-flash", 100000)

	doc := extraction.Result{
		Path:   "/docs/contract.pdf",
		Method: "pdftotext",
		Pages:  []output.PDFPage{{Number: 1, Text: "Стороны: ООО А и ООО Б"}},
	}
	dig, err := w.Digest(context.Background(), doc)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if dig.Path != "/docs/contract.pdf" || dig.Method != "pdftotext" {
		t.Fatalf("digest metadata wrong: %+v", dig)
	}
	if !strings.Contains(dig.Text, "стр. 1") {
		t.Fatalf("digest must carry page cite, got %q", dig.Text)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("want 1 runner call, got %d", len(runner.tasks))
	}
	if !strings.Contains(runner.tasks[0], "/docs/contract.pdf") ||
		!strings.Contains(runner.tasks[0], "Страница 1") {
		t.Fatalf("task missing path/page markers: %q", runner.tasks[0])
	}
	if len(runner.cfgs[0].ToolNames) != 0 || runner.cfgs[0].Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected subagent config: %+v", runner.cfgs[0])
	}
}

func TestDigest_MultipleBatchesAreJoined(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"часть1", "часть2"}}
	w := NewDigestWorker(runner, "m", 10) // tiny budget forces 2 batches

	doc := extraction.Result{
		Path: "/docs/big.pdf",
		Pages: []output.PDFPage{
			{Number: 1, Text: strings.Repeat("a", 8)},
			{Number: 2, Text: strings.Repeat("b", 8)},
		},
	}
	dig, err := w.Digest(context.Background(), doc)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if len(runner.tasks) != 2 {
		t.Fatalf("want 2 runner calls, got %d", len(runner.tasks))
	}
	if !strings.Contains(dig.Text, "часть1") || !strings.Contains(dig.Text, "часть2") {
		t.Fatalf("joined digest missing parts: %q", dig.Text)
	}
}

func TestDigest_EmptyPagesErrors(t *testing.T) {
	w := NewDigestWorker(&fakeRunner{}, "m", 100)
	_, err := w.Digest(context.Background(), extraction.Result{Path: "/d/empty.pdf"})
	if err == nil {
		t.Fatalf("want error when document has no pages")
	}
}

func TestDigest_RunnerErrorPropagates(t *testing.T) {
	runner := &fakeRunner{err: errors.New("llm down")}
	w := NewDigestWorker(runner, "m", 100)
	doc := extraction.Result{Path: "/d/x.pdf", Pages: []output.PDFPage{{Number: 1, Text: "t"}}}
	if _, err := w.Digest(context.Background(), doc); err == nil {
		t.Fatalf("want error from runner")
	}
}

func TestDigest_EmptyOutputErrors(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"   ", "   "}} // blank twice = doc effectively unread
	w := NewDigestWorker(runner, "m", 100)
	doc := extraction.Result{Path: "/d/x.pdf", Pages: []output.PDFPage{{Number: 1, Text: "t"}}}
	if _, err := w.Digest(context.Background(), doc); err == nil {
		t.Fatalf("want error on empty digest output")
	}
}

// A zero budget must fall back to the default, not split every page into its own
// LLM call (a silent cost/latency explosion for big documents).
func TestDigest_ZeroBudgetFallsBackToDefault(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"ok"}}
	w := NewDigestWorker(runner, "m", 0)
	doc := extraction.Result{Path: "/d/x.pdf", Pages: []output.PDFPage{
		{Number: 1, Text: "a"}, {Number: 2, Text: "b"}, {Number: 3, Text: "c"},
	}}
	if _, err := w.Digest(context.Background(), doc); err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("want a single batched call under default budget, got %d", len(runner.tasks))
	}
}

// A reasoning model that burns its whole budget on hidden thinking returns an
// empty completion for a big batch. The batch is halved and retried, not the
// whole document thrown away.
func TestDigest_EmptyBatchIsSplitAndRetried(t *testing.T) {
	// budget 10 chars, 6-char pages -> 1 page per batch... so use 4-char pages: 2 per batch.
	pages := []output.PDFPage{
		{Number: 1, Text: "aaaa"}, {Number: 2, Text: "bbbb"},
		{Number: 3, Text: "cccc"}, {Number: 4, Text: "dddd"},
	}
	runner := &fakeRunner{outputs: []string{"d12", "", "d3", "d4"}}
	w := NewDigestWorker(runner, "m", 9)
	got, err := w.Digest(context.Background(), extraction.Result{Path: "/d/x.pdf", Pages: pages})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if len(runner.tasks) != 4 {
		t.Fatalf("want 4 calls (2 batches, 1 split into halves), got %d", len(runner.tasks))
	}
	for _, part := range []string{"d12", "d3", "d4"} {
		if !strings.Contains(got.Text, part) {
			t.Fatalf("digest %q lacks %q", got.Text, part)
		}
	}
	if !strings.Contains(runner.tasks[2], "Страница 3") || strings.Contains(runner.tasks[2], "Страница 4") {
		t.Fatalf("second half must be page 3 alone, task: %q", runner.tasks[2])
	}
}

func TestDigest_UnreadPartIsMarkedNotFatal(t *testing.T) {
	pages := []output.PDFPage{{Number: 1, Text: "aaaa"}, {Number: 2, Text: "bbbb"}}
	// batch1 ok; batch2 (one page) empty, retry empty -> placeholder.
	runner := &fakeRunner{outputs: []string{"d1", "", ""}}
	w := NewDigestWorker(runner, "m", 5)
	got, err := w.Digest(context.Background(), extraction.Result{Path: "/d/x.pdf", Pages: pages})
	if err != nil {
		t.Fatalf("one unread part must not fail the document: %v", err)
	}
	if !strings.Contains(got.Text, "d1") || !strings.Contains(got.Text, "не прочитан") || !strings.Contains(got.Text, "стр. 2") {
		t.Fatalf("digest = %q", got.Text)
	}
	if len(runner.tasks) != 3 {
		t.Fatalf("want batch + retry, got %d calls", len(runner.tasks))
	}
}
