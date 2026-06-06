package legalreview

import (
	"context"
	"fmt"
	"sync"

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

const defaultConcurrency = 4

// Orchestrator прогоняет пачку путей через извлечение+выжимку с ограниченной
// конкуррентностью и сводит результат координатором.
type Orchestrator struct {
	extractor   extractor
	digester    digester
	reviewer    reviewer
	concurrency int
}

func NewOrchestrator(e extractor, d digester, r reviewer, concurrency int) *Orchestrator {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	return &Orchestrator{extractor: e, digester: d, reviewer: r, concurrency: concurrency}
}

// Review обрабатывает пачку и возвращает текст отчёта. Падение на одном документе
// не валит пачку — документ помечается «не прочитан» (пустой Text) и всё равно
// уходит координатору, чтобы пропавший документ не убрал молча юр-вывод. Если не
// прочитан НИ ОДИН документ — премиум-координатор не зовём, возвращаем ошибку.
func (o *Orchestrator) Review(ctx context.Context, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("orchestrator: no documents to review")
	}

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

	anyRead := false
	for _, d := range digests {
		if d.Text != "" {
			anyRead = true
			break
		}
	}
	if !anyRead {
		return "", fmt.Errorf("orchestrator: no documents could be read (%d failed)", len(paths))
	}

	return o.reviewer.Review(ctx, digests)
}

// processOne извлекает и выжимает один документ. Любая ошибка → «не прочитан»
// (Path сохранён, Text пуст), а не потеря документа из пачки.
func (o *Orchestrator) processOne(ctx context.Context, path string) Digest {
	res, err := o.extractor.Extract(ctx, path)
	if err != nil {
		return Digest{Path: path}
	}
	dig, err := o.digester.Digest(ctx, res)
	if err != nil {
		return Digest{Path: path}
	}
	return dig
}
