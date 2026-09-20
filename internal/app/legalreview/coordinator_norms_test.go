package legalreview

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeNorms struct {
	out   string
	err   error
	texts []string
	limit int
}

func (f *fakeNorms) Excerpts(_ context.Context, texts []string, limit int) (string, error) {
	f.texts, f.limit = texts, limit
	return f.out, f.err
}

func TestCoordinator_ExcerptsPrependedToBody(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"отчёт"}}
	c := NewCoordinator(runner, "sonnet", "flash", "оглавление", 80000)
	norms := &fakeNorms{out: "[218-ФЗ, ст. 26 ч. 1 п. 7] «форма документа»"}
	c.SetNormRetriever(norms)

	_, err := c.Review(context.Background(), []Digest{
		{Path: "/d/a.pdf", Text: "замечание по п. 7 ч. 1 ст. 26 218-ФЗ (стр. 1)"},
		{Path: "/d/empty.pdf", Text: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(norms.texts) != 1 || norms.limit != excerptsLimit {
		t.Fatalf("unread digests must not be sent to the corpus: %v limit=%d", norms.texts, norms.limit)
	}
	body := runner.tasks[0]
	if !strings.HasPrefix(body, excerptsHeader) || !strings.Contains(body, "«форма документа»") {
		t.Fatalf("excerpts missing at the top:\n%s", body)
	}
	if strings.Index(body, "ВЫДЕРЖКИ") > strings.Index(body, "===== ДОКУМЕНТ") {
		t.Fatal("excerpts must precede the digests")
	}
	if !strings.Contains(runner.cfgs[0].SystemPrompt, "ИСТОЧНИК НОРМ") {
		t.Fatal("rule 8 missing from the system prompt")
	}
}

func TestCoordinator_NoRetrieverOrEmptyExcerpts(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"отчёт", "отчёт"}}
	c := NewCoordinator(runner, "sonnet", "flash", "", 80000)
	if _, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.tasks[0], "ВЫДЕРЖКИ") {
		t.Fatal("no retriever → no excerpts section")
	}

	c.SetNormRetriever(&fakeNorms{out: "   "})
	if _, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.tasks[1], "ВЫДЕРЖКИ") {
		t.Fatal("blank excerpts → no section")
	}
}

func TestCoordinator_ExcerptsErrorIsNotFatal(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"отчёт"}}
	c := NewCoordinator(runner, "sonnet", "flash", "", 80000)
	c.SetNormRetriever(&fakeNorms{err: errors.New("db down")})
	out, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}})
	if err != nil || out != "отчёт" {
		t.Fatalf("review must survive a corpus failure: %v %q", err, out)
	}
}

func TestCoordinator_ExcerptsTruncatedNotDigests(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"отчёт"}}
	digest := strings.Repeat("д", 400) // ~200 tokens by the chars/4 estimate (2-byte runes)
	// Budget = system prompt + digest body + a little room, so excerpts must be cut.
	probe := NewCoordinator(runner, "sonnet", "flash", "", 1)
	budget := estimateTokens(probe.systemPrompt()) + estimateTokens(formatDigests([]Digest{{Path: "/d/a.pdf", Text: digest}})) + 100
	c := NewCoordinator(runner, "sonnet", "flash", "", budget)
	c.SetNormRetriever(&fakeNorms{out: strings.Repeat("н", 4000)}) // far over the remaining room

	_, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: digest}})
	if err != nil {
		t.Fatal(err)
	}
	body := runner.tasks[0]
	if !strings.Contains(body, digest) {
		t.Fatal("digest must never be cut for excerpts")
	}
	if !strings.Contains(body, "обрезаны по бюджету") {
		t.Fatal("excerpts should be truncated with a marker")
	}
	marker := "\n[выдержки обрезаны по бюджету контекста]"
	if estimateTokens(body)+estimateTokens(c.systemPrompt()) > budget+estimateTokens(marker)+2 {
		t.Fatalf("body over budget: %d tokens", estimateTokens(body))
	}
}
