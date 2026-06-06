package legalreview

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEstimateTokens_CharsOverFour(t *testing.T) {
	if got := estimateTokens(strings.Repeat("x", 400)); got != 100 {
		t.Fatalf("want 100 tokens for 400 chars, got %d", got)
	}
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("want 0 for empty, got %d", got)
	}
}

func TestFormatDigests_HeadersAndProvenance(t *testing.T) {
	body := formatDigests([]Digest{
		{Path: "/d/a.pdf", Method: "pdftotext", Text: "факт A (стр. 1)"},
		{Path: "/d/b.pdf", Method: "vision", Text: "факт B (стр. 2)"},
	})
	for _, want := range []string{"/d/a.pdf", "pdftotext", "факт A (стр. 1)", "/d/b.pdf", "vision", "факт B (стр. 2)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

// Непрочитанный документ обязан оставить ЯВНЫЙ след в теле, иначе координатор
// молча потеряет юр-вывод по этому документу (дизайн: «не прочитан»).
func TestFormatDigests_EmptyTextMarkedUnread(t *testing.T) {
	body := formatDigests([]Digest{{Path: "/d/x.pdf", Text: "   "}})
	if !strings.Contains(body, "/d/x.pdf") || !strings.Contains(body, "не прочитан") {
		t.Fatalf("empty digest must be marked unread, got:\n%s", body)
	}
}

func TestCoordinator_SingleSonnetCallUnderBudget(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"ЗАКЛЮЧЕНИЕ ПО ТЕХПЛАНУ: расхождение площади (файл /d/a.pdf, стр. 1)"}}
	c := NewCoordinator(runner, "anthropic/claude-sonnet-4.6", "deepseek/deepseek-v4-flash", "СП 1: площадь считается так-то.", 100000)

	report, err := c.Review(context.Background(), []Digest{
		{Path: "/d/a.pdf", Method: "pdftotext", Text: "Площадь 120 м2 (стр. 1)"},
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(report, "ЗАКЛЮЧЕНИЕ ПО ТЕХПЛАНУ") {
		t.Fatalf("report missing, got %q", report)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("want exactly 1 LLM call under budget, got %d", len(runner.tasks))
	}
	cfg := runner.cfgs[0]
	if cfg.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("coordinator must call Sonnet, got %q", cfg.Model)
	}
	if len(cfg.ToolNames) != 0 || cfg.MaxTurns != 1 {
		t.Fatalf("coordinator must be tool-less single-turn, got %+v", cfg)
	}
	// Нормативка обязана попасть в системный промпт (стабильный кэш-префикс).
	if !strings.Contains(cfg.SystemPrompt, "СП 1: площадь считается так-то.") {
		t.Fatalf("normativy must be inlined in system prompt")
	}
	// Тело запроса несёт выжимку с привязкой к странице.
	if !strings.Contains(runner.tasks[0], "/d/a.pdf") || !strings.Contains(runner.tasks[0], "стр. 1") {
		t.Fatalf("task body missing digest/page cite: %q", runner.tasks[0])
	}
}

func TestCoordinator_EmptyDigestsErrors(t *testing.T) {
	c := NewCoordinator(&fakeRunner{}, "m", "r", "", 1000)
	if _, err := c.Review(context.Background(), nil); err == nil {
		t.Fatalf("want error when no digests to review")
	}
}

func TestCoordinator_RunnerErrorPropagates(t *testing.T) {
	c := NewCoordinator(&fakeRunner{err: errors.New("sonnet down")}, "m", "r", "", 1000)
	_, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}})
	if err == nil {
		t.Fatalf("want error from runner")
	}
}

func TestCoordinator_EmptyReportErrors(t *testing.T) {
	c := NewCoordinator(&fakeRunner{outputs: []string{"   "}}, "m", "r", "", 1000)
	_, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: "t"}})
	if err == nil {
		t.Fatalf("want error on empty report")
	}
}
