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

// groupDigests должен резать список по символьному бюджету; одна большая
// выжимка получает свою группу, не дробится ниже документа.
func TestGroupDigests_SplitsByCharBudget(t *testing.T) {
	digests := []Digest{
		{Path: "a", Text: strings.Repeat("a", 6)},
		{Path: "b", Text: strings.Repeat("b", 6)},
		{Path: "c", Text: strings.Repeat("c", 6)},
	}
	groups := groupDigests(digests, 10) // по 6 символов: пары переполняют → по одному
	if len(groups) != 3 {
		t.Fatalf("want 3 groups, got %d", len(groups))
	}
}

func TestGroupDigests_EmptyIsNil(t *testing.T) {
	if g := groupDigests(nil, 100); g != nil {
		t.Fatalf("want nil for no digests, got %+v", g)
	}
}

// Над бюджетом: координатор сперва гоняет reduce-проход(ы) на ДЕШЁВОЙ модели,
// а финальный вызов всё равно на Sonnet и уже под бюджетом.
func TestCoordinator_OverBudgetReducesThenSonnet(t *testing.T) {
	// Бюджет крошечный → reduce обязателен. Системный промпт сам по себе уже
	// съест часть; ставим budget так, чтобы две большие выжимки переполнили.
	runner := &fakeRunner{outputs: []string{"сжато1", "сжато2", "ИТОГ: отчёт"}}
	c := NewCoordinator(runner, "sonnet", "deepseek-flash", "", 50)

	big := strings.Repeat("ц", 4000) // ~1000 токенов каждая
	report, err := c.Review(context.Background(), []Digest{
		{Path: "/d/a.pdf", Text: big},
		{Path: "/d/b.pdf", Text: big},
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(report, "ИТОГ") {
		t.Fatalf("final report missing, got %q", report)
	}
	if len(runner.cfgs) < 2 {
		t.Fatalf("want at least one reduce call + final, got %d calls", len(runner.cfgs))
	}
	// Последний вызов — Sonnet; до него — reduce на дешёвой модели.
	last := runner.cfgs[len(runner.cfgs)-1]
	if last.Model != "sonnet" {
		t.Fatalf("final call must be Sonnet, got %q", last.Model)
	}
	sawReduce := false
	for _, cfg := range runner.cfgs[:len(runner.cfgs)-1] {
		if cfg.Model == "deepseek-flash" {
			sawReduce = true
		}
	}
	if !sawReduce {
		t.Fatalf("want a reduce pass on the cheap model before Sonnet")
	}
}

// Защита от зацикливания: если reduce не может ужать (одна выжимка крупнее
// бюджета), координатор всё равно завершает одним вызовом Sonnet, а не виснет.
func TestCoordinator_NonConvergingReduceStillTerminates(t *testing.T) {
	runner := &fakeRunner{outputs: []string{"ИТОГ"}}
	c := NewCoordinator(runner, "sonnet", "deepseek-flash", "", 1) // бюджет 1 токен — недостижим
	_, err := c.Review(context.Background(), []Digest{{Path: "/d/a.pdf", Text: strings.Repeat("я", 8000)}})
	if err != nil {
		t.Fatalf("Review must terminate, got %v", err)
	}
	last := runner.cfgs[len(runner.cfgs)-1]
	if last.Model != "sonnet" {
		t.Fatalf("must still finish on Sonnet, got %q", last.Model)
	}
}
