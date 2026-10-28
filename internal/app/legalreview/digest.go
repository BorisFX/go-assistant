package legalreview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/extraction"
	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// chunkPages groups consecutive pages into batches whose combined text stays
// under maxChars. A single page larger than the budget gets its own batch (we
// don't split below page granularity). Returns nil for no pages.
func chunkPages(pages []output.PDFPage, maxChars int) [][]output.PDFPage {
	if len(pages) == 0 {
		return nil
	}
	var (
		batches [][]output.PDFPage
		cur     []output.PDFPage
		curLen  int
	)
	for _, p := range pages {
		n := len(p.Text)
		if len(cur) > 0 && curLen+n > maxChars {
			batches = append(batches, cur)
			cur, curLen = nil, 0
		}
		cur = append(cur, p)
		curLen += n
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// digestSystemPrompt frames the worker's job: faithfully report what a single
// document contains, with verbatim quotes and page cites — NOT to judge legality
// (that is the coordinator's job). Keeps the later verdict disciplined.
const digestSystemPrompt = `Ты — аналитик, готовящий ДАЙДЖЕСТ одного документа для последующей юридической проверки строительной документации. Не делай юридических выводов и не оценивай соответствие нормам — это сделает координатор. Твоя задача — точно и сжато изложить, ЧТО в документе есть.

Правила:
1. Извлеки все юридически значимые факты: реквизиты (номера, даты, стороны), кадастровые/адресные данные, площади, этажность, материалы, отметки, штампы, ссылки на нормы, суммы, сроки.
2. Каждый ключевой факт подкрепляй ДОСЛОВНОЙ цитатой и ссылкой на страницу в формате «(стр. N)».
3. Ничего не выдумывай. Если данные нечитаемы или отсутствуют — так и напиши.
4. Не пиши «законно/незаконно» и не давай заключений — только факты с цитатами.
5. ТАБЛИЦЫ И ГРАФИКИ ЦИТИРУЙ СТРОКОЙ ЦЕЛИКОМ. Если факт взят из строки таблицы (календарный план, ведомость, экспликация), приводи всю строку одной цитатой: наименование, длительность, даты, номер — вместе. Числа из соседних строк НЕ СМЕШИВАЙ: «90 дней» одной работы и даты другой, склеенные в один факт, дают ложное замечание.
6. Формат: краткое РЕЗЮМЕ (2–4 строки), затем список фактов с цитатами.`

const digestMaxTokens = 8192

// defaultMaxChars is the fallback worker budget (~12k tokens at chars≈tokens×4).
// Guards against a zero budget silently producing one LLM call per page.
const defaultMaxChars = 48000

// subagentRunner is the slice of *subagent.Runner the worker needs, isolated as
// an interface so the worker is testable without a real LLM.
type subagentRunner interface {
	Run(ctx context.Context, cfg subagent.Config, task string) (string, error)
}

// Digest is one document's structured summary with page-cited verbatim quotes.
type Digest struct {
	Path   string
	Method string // extraction provenance from B1: "pdftotext" | "vision" | "mistral-ocr" | "signature" | ...
	Text   string
	// Facts is the structured skeleton used by the Go-side cross-check; empty
	// when extraction was skipped or failed.
	Facts Facts
	// Partial marks a digest with unread pages; such digests are never cached
	// so a rerun gets another chance at the missing parts.
	Partial bool `json:"partial,omitempty"`
}

// DigestWorker turns an extracted document into a cheap, disciplined digest by
// running a tool-less subagent over the document text.
type DigestWorker struct {
	runner   subagentRunner
	model    string
	maxChars int
}

func NewDigestWorker(runner subagentRunner, model string, maxChars int) *DigestWorker {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	return &DigestWorker{runner: runner, model: model, maxChars: maxChars}
}

// Digest summarizes one document. Oversized documents are paged into batches
// that each fit the worker budget; their digests are concatenated. An empty
// document or an empty model reply is an error so the orchestrator can mark the
// document "не прочитан" instead of silently dropping it.
func (w *DigestWorker) Digest(ctx context.Context, doc extraction.Result) (Digest, error) {
	if len(doc.Pages) == 0 {
		return Digest{}, fmt.Errorf("digest %q: no pages to digest", doc.Path)
	}
	cfg := subagent.Config{
		Model:        w.model,
		SystemPrompt: digestSystemPrompt,
		MaxTurns:     1,
		Temperature:  0,
		MaxTokens:    digestMaxTokens,
	}
	start := time.Now()
	batches := chunkPages(doc.Pages, w.maxChars)
	var parts []string
	st := &batchStats{}
	for i, batch := range batches {
		out, err := w.digestBatch(ctx, cfg, doc.Path, batch, splitDepth, st)
		if err != nil {
			return Digest{}, fmt.Errorf("digest %q batch %d: %w", doc.Path, i, err)
		}
		parts = append(parts, out)
	}
	if st.read == 0 {
		return Digest{}, fmt.Errorf("digest %q: empty model output for every part", doc.Path)
	}
	text := strings.Join(parts, "\n\n")
	if st.unread > 0 {
		slog.Warn("legalreview digest: parts unread", "path", doc.Path, "unread_parts", st.unread, "read_parts", st.read)
	}
	slog.Info("legalreview digest",
		"path", doc.Path, "model", w.model, "method", doc.Method,
		"pages", len(doc.Pages), "batches", len(batches), "calls", st.calls,
		"in_chars", st.inChars, "out_chars", len(text),
		"ms", time.Since(start).Milliseconds())
	return Digest{Path: doc.Path, Method: doc.Method, Text: text, Partial: st.unread > 0}, nil
}

// splitDepth bounds how many times an empty batch is halved before the
// remaining pages are retried as-is and, failing that, marked unread.
const splitDepth = 2

type batchStats struct {
	calls, retries, inChars int
	read, unread            int
}

// digestBatch runs one batch and copes with an empty completion — a reasoning
// model that spent its whole budget thinking. A multi-page batch is halved and
// each half digested on its own; a single page (or an exhausted split budget)
// gets one plain retry. What still comes back empty is marked in the digest
// as unread pages, so the coordinator sees the gap instead of a silently
// dropped document.
func (w *DigestWorker) digestBatch(ctx context.Context, cfg subagent.Config, path string, pages []output.PDFPage, depth int, st *batchStats) (string, error) {
	task := buildDigestTask(path, pages)
	st.calls++
	st.inChars += len(task)
	out, err := w.runner.Run(ctx, cfg, task)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) != "" {
		st.read++
		return strings.TrimSpace(out), nil
	}

	if len(pages) > 1 && depth > 0 {
		slog.Warn("legalreview digest: empty batch, splitting", "path", path, "pages", len(pages))
		mid := len(pages) / 2
		left, err := w.digestBatch(ctx, cfg, path, pages[:mid], depth-1, st)
		if err != nil {
			return "", err
		}
		right, err := w.digestBatch(ctx, cfg, path, pages[mid:], depth-1, st)
		if err != nil {
			return "", err
		}
		return left + "\n\n" + right, nil
	}

	st.calls++
	st.retries++
	st.inChars += len(task)
	out, err = w.runner.Run(ctx, cfg, task)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) != "" {
		st.read++
		return strings.TrimSpace(out), nil
	}
	st.unread++
	first, last := pages[0].Number, pages[len(pages)-1].Number
	slog.Warn("legalreview digest: pages unread", "path", path, "from", first, "to", last)
	if first == last {
		return fmt.Sprintf("[стр. %d не прочитана: модель не вернула выжимку]", first), nil
	}
	return fmt.Sprintf("[стр. %d–%d не прочитаны: модель не вернула выжимку]", first, last), nil
}

// buildDigestTask renders a batch of pages into the subagent task, keeping page
// markers so the model can cite "(стр. N)".
func buildDigestTask(path string, pages []output.PDFPage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Документ: %s\n\n", path)
	for _, p := range pages {
		fmt.Fprintf(&b, "=== Страница %d ===\n%s\n\n", p.Number, p.Text)
	}
	b.WriteString("Составь структурированный дайджест этого документа по инструкции из системного промпта.")
	return b.String()
}

// Compile-time check that the real subagent runner satisfies the worker's needs.
var _ subagentRunner = (*subagent.Runner)(nil)
