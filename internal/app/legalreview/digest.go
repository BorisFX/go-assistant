package legalreview

import (
	"context"
	"fmt"
	"strings"

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
5. Формат: краткое РЕЗЮМЕ (2–4 строки), затем список фактов с цитатами.`

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
	Method string // extraction provenance from B1: "pdftotext" | "vision" | "mistral-ocr"
	Text   string
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
	var parts []string
	for i, batch := range chunkPages(doc.Pages, w.maxChars) {
		out, err := w.runner.Run(ctx, cfg, buildDigestTask(doc.Path, batch))
		if err != nil {
			return Digest{}, fmt.Errorf("digest %q batch %d: %w", doc.Path, i, err)
		}
		if strings.TrimSpace(out) == "" {
			return Digest{}, fmt.Errorf("digest %q batch %d: empty model output", doc.Path, i)
		}
		parts = append(parts, strings.TrimSpace(out))
	}
	return Digest{Path: doc.Path, Method: doc.Method, Text: strings.Join(parts, "\n\n")}, nil
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
