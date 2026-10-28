package legalreview

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	Review(ctx context.Context, digests []Digest) (Conclusion, error)
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
	ManifestDigestPath   = "СОСТАВ ПАКЕТА"
)

// ReviewRequest is one batch to review: the documents and, optionally, what
// the user asked to look at.
type ReviewRequest struct {
	Paths []string
	Focus string
	// Context is the recent chat rendered by RenderContext: what the user told
	// the assistant about the object before asking for the review.
	Context string
	// OnProgress, when set, receives stage and per-document events so a caller
	// can show that a long run is alive.
	OnProgress Progress
}

// ReviewResult carries the report together with per-document provenance, so
// the client report can say how each file was read (or that it was not).
type ReviewResult struct {
	Report  string
	Digests []Digest
	// CrossCheck is the deterministic comparison block, kept so the report can
	// print it verbatim instead of trusting the coordinator's retelling.
	CrossCheck string
	// Unverified lists the report's numbers that no source backs.
	Unverified []Unverified
}

// Orchestrator прогоняет пачку путей через извлечение+выжимку с ограниченной
// конкуррентностью и сводит результат координатором.
type Orchestrator struct {
	extractor   extractor
	digester    digester
	reviewer    reviewer
	facts       factsExtractor
	concurrency int
	cache       *digestCache
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

// WithCache stores finished digests on disk keyed by file content, so a batch
// that failed at the coordinator (or is asked about again) does not pay for
// its digests twice. keySalt names the models in use: a model change must
// miss the cache, and so does a change of the worker prompts.
func (o *Orchestrator) WithCache(dir, keySalt string) *Orchestrator {
	o.cache = newDigestCache(dir, keySalt+"|"+HashText(digestSystemPrompt+factsSystemPrompt))
	return o
}

// Review обрабатывает пачку и возвращает текст отчёта. Kept for callers that
// have no focus; see ReviewRequest.
func (o *Orchestrator) Review(ctx context.Context, paths []string) (string, error) {
	res, err := o.ReviewRequest(ctx, ReviewRequest{Paths: paths})
	return res.Report, err
}

// ReviewRequest обрабатывает пачку и возвращает текст отчёта. Падение на одном
// документе не валит пачку — документ помечается «не прочитан» (пустой Text) и
// всё равно уходит координатору, чтобы пропавший документ не убрал молча
// юр-вывод. Если не прочитан НИ ОДИН документ — премиум-координатор не зовём,
// возвращаем ошибку.
func (o *Orchestrator) ReviewRequest(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	paths := req.Paths
	if len(paths) == 0 {
		return ReviewResult{}, fmt.Errorf("orchestrator: no documents to review")
	}
	start := time.Now()
	slog.Info("legalreview start", "documents", len(paths), "concurrency", o.concurrency, "focus", req.Focus != "")

	req.OnProgress.emit(ProgressEvent{Stage: StageDigest, Done: 0, Total: len(paths)})

	digests := make([]Digest, len(paths))
	sem := make(chan struct{}, o.concurrency)
	var wg sync.WaitGroup
	var doneCount int64
	for i, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			digests[i] = o.processOne(ctx, path)
			req.OnProgress.emit(ProgressEvent{
				Stage:  StageDigest,
				Done:   int(atomic.AddInt64(&doneCount, 1)),
				Total:  len(paths),
				Detail: filepath.Base(path),
			})
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
		return ReviewResult{Digests: digests}, fmt.Errorf("orchestrator: no documents could be read (%d failed)", len(paths))
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
	if c := strings.TrimSpace(req.Context); c != "" {
		head = append(head, Digest{Path: ContextDigestPath, Method: "user", Text: c})
	}
	head = append(head, Digest{Path: ManifestDigestPath, Method: "go", Text: renderManifest(digests)})

	req.OnProgress.emit(ProgressEvent{Stage: StageCrossCheck, Done: read, Total: len(paths)})

	crossCheck := Collide(digests)
	if crossCheck != "" {
		head = append(head, Digest{Path: CollisionsDigestPath, Method: "go", Text: crossCheck})
	}
	all := append(head, digests...)

	req.OnProgress.emit(ProgressEvent{Stage: StageCoordinator, Done: read, Total: len(paths)})

	conclusion, err := o.reviewer.Review(ctx, all)
	if err != nil {
		return ReviewResult{Digests: digests, CrossCheck: crossCheck}, err
	}
	report := conclusion.Report

	// Everything the coordinator could legitimately draw a number from.
	evidence := make([]string, 0, len(all)+len(conclusion.Evidence))
	for _, d := range all {
		if d.Text != "" {
			evidence = append(evidence, d.Text)
		}
	}
	evidence = append(evidence, conclusion.Evidence...)
	unverified := Verify(report, evidence)
	if len(unverified) > 0 {
		slog.Warn("legalreview: report contains values no source backs",
			"count", len(unverified), "values", unverifiedValues(unverified))
	}
	req.OnProgress.emit(ProgressEvent{Stage: StageReport, Done: read, Total: len(paths)})
	slog.Info("legalreview done",
		"report_chars", len(report), "unverified", len(unverified),
		"ms", time.Since(start).Milliseconds())
	return ReviewResult{Report: report, Digests: digests, CrossCheck: crossCheck, Unverified: unverified}, nil
}

// processOne извлекает и выжимает один документ. Любая ошибка → «не прочитан»
// (Path сохранён, Text пуст), а не потеря документа из пачки. The facts pass
// runs on the digest, never on the raw document, and its failure is silent.
func (o *Orchestrator) processOne(ctx context.Context, path string) Digest {
	if d, ok := o.cache.Get(path); ok {
		slog.Info("legalreview digest from cache", "path", path)
		return d
	}
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
	if !dig.Partial {
		o.cache.Put(path, dig)
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

// unverifiedValues renders the flagged values for one log line.
func unverifiedValues(list []Unverified) string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		out = append(out, u.Value)
	}
	return strings.Join(out, ", ")
}

// renderManifest is the authoritative roster of the batch, built in Go from
// real extraction provenance. The reduce pass squeezes forty digests into
// three blocks and files drop out of the squeeze; without this roster the
// coordinator reads their absence as «не прочитан» and says so in a report
// whose own file table says the opposite.
func renderManifest(digests []Digest) string {
	var b strings.Builder
	b.WriteString("Полный состав пачки и что из неё удалось прочитать. Это единственный источник статуса документа.\n\n")
	for i, d := range digests {
		status := "прочитан"
		if strings.TrimSpace(d.Text) == "" {
			status = "НЕ прочитан"
		}
		method := d.Method
		if method == "" {
			method = "—"
		}
		fmt.Fprintf(&b, "%d. %s — %s (%s)\n", i+1, filepath.Base(d.Path), status, method)
	}
	return strings.TrimRight(b.String(), "\n")
}
