package legalreview

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/extraction"
)

// Зависимости ядра — интерфейсы, чтобы оркестратор тестировался без LLM/OCR.
type extractor interface {
	Extract(ctx context.Context, path string) (extraction.Result, error)
}
type digester interface {
	Digest(ctx context.Context, doc extraction.Result) (Digest, error)
}
type reviewer interface {
	Review(ctx context.Context, digests []Digest) (string, error)
}

// factsExtractor turns a digest into comparable structured facts. Optional:
// without it the batch skips the Go-side cross-check.
type factsExtractor interface {
	Extract(ctx context.Context, d Digest) Facts
}

const defaultConcurrency = 4

// Pseudo-document paths the orchestrator prepends for the coordinator. They
// are not files: one carries the user's question, the other the deterministic
// cross-check of the batch.
const (
	FocusDigestPath      = "ЗАДАЧА ОТ ПОЛЬЗОВАТЕЛЯ"
	CollisionsDigestPath = "АВТОСВЕРКА"
)

// ReviewRequest is one batch to review: the documents and, optionally, what
// the user asked to look at.
type ReviewRequest struct {
	Paths []string
	Focus string
}

// Orchestrator прогоняет пачку путей через извлечение+выжимку с ограниченной
// конкуррентностью и сводит результат координатором.
type Orchestrator struct {
	extractor   extractor
	digester    digester
	reviewer    reviewer
	facts       factsExtractor
	concurrency int
}

func NewOrchestrator(e extractor, d digester, r reviewer, concurrency int) *Orchestrator {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	return &Orchestrator{extractor: e, digester: d, reviewer: r, concurrency: concurrency}
}

// WithFacts enables the structured-facts pass and the cross-check built on it.
func (o *Orchestrator) WithFacts(f factsExtractor) *Orchestrator {
	o.facts = f
	return o
}

// Review обрабатывает пачку и возвращает текст отчёта. Kept for callers that
// have no focus; see ReviewRequest.
func (o *Orchestrator) Review(ctx context.Context, paths []string) (string, error) {
	return o.ReviewRequest(ctx, ReviewRequest{Paths: paths})
}

// ReviewRequest обрабатывает пачку и возвращает текст отчёта. Падение на одном
// документе не валит пачку — документ помечается «не прочитан» (пустой Text) и
// всё равно уходит координатору, чтобы пропавший документ не убрал молча
// юр-вывод. Если не прочитан НИ ОДИН документ — премиум-координатор не зовём,
// возвращаем ошибку.
func (o *Orchestrator) ReviewRequest(ctx context.Context, req ReviewRequest) (string, error) {
	paths := req.Paths
	if len(paths) == 0 {
		return "", fmt.Errorf("orchestrator: no documents to review")
	}
	start := time.Now()
	slog.Info("legalreview start", "documents", len(paths), "concurrency", o.concurrency, "focus", req.Focus != "")

	digests := make([]Digest, len(paths))
	sem := make(chan struct{}, o.concurrency)
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			digests[i] = o.processOne(ctx, path)
		}(i, path)
	}
	wg.Wait()

	read := 0
	for _, d := range digests {
		if d.Text != "" {
			read++
		}
	}
	if read == 0 {
		return "", fmt.Errorf("orchestrator: no documents could be read (%d failed)", len(paths))
	}
	slog.Info("legalreview digests ready",
		"total", len(paths), "read", read, "unread", len(paths)-read,
		"ms", time.Since(start).Milliseconds())

	// Pseudo-documents go first so the coordinator reads the question and the
	// deterministic cross-check before the evidence.
	var head []Digest
	if focus := req.Focus; focus != "" {
		head = append(head, Digest{Path: FocusDigestPath, Method: "user", Text: focus})
	}
	if block := Collide(digests); block != "" {
		head = append(head, Digest{Path: CollisionsDigestPath, Method: "go", Text: block})
	}
	all := append(head, digests...)

	report, err := o.reviewer.Review(ctx, all)
	if err != nil {
		return "", err
	}
	slog.Info("legalreview done",
		"report_chars", len(report), "ms", time.Since(start).Milliseconds())
	return report, nil
}

// processOne извлекает и выжимает один документ. Любая ошибка → «не прочитан»
// (Path сохранён, Text пуст), а не потеря документа из пачки. The facts pass
// runs on the digest, never on the raw document, and its failure is silent.
func (o *Orchestrator) processOne(ctx context.Context, path string) Digest {
	res, err := o.extractor.Extract(ctx, path)
	if err != nil {
		slog.Warn("legalreview unread: extract failed", "path", path, "error", err)
		return Digest{Path: path}
	}
	dig, err := o.digester.Digest(ctx, res)
	if err != nil {
		slog.Warn("legalreview unread: digest failed", "path", path, "error", err)
		return Digest{Path: path}
	}
	if o.facts != nil {
		dig.Facts = o.facts.Extract(ctx, dig)
	}
	return dig
}

// Compile-time: реальные реализации удовлетворяют интерфейсам ядра.
var (
	_ extractor      = (*extraction.Router)(nil)
	_ digester       = (*DigestWorker)(nil)
	_ reviewer       = (*Coordinator)(nil)
	_ factsExtractor = (*FactsExtractor)(nil)
)
