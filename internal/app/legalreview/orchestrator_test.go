package legalreview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/extraction"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// fakeExtractor returns a scripted Result (or error) per path.
type fakeExtractor struct {
	byPath map[string]extraction.Result
	errs   map[string]error
}

func (f *fakeExtractor) Extract(_ context.Context, path string) (extraction.Result, error) {
	if err := f.errs[path]; err != nil {
		return extraction.Result{}, err
	}
	if r, ok := f.byPath[path]; ok {
		return r, nil
	}
	return extraction.Result{Path: path, Pages: []output.PDFPage{{Number: 1, Text: "x"}}}, nil
}

// fakeDigester turns a Result into a Digest echoing the path; can error per path.
type fakeDigester struct {
	errs     map[string]error
	inFlight int32
	maxSeen  int32
}

func (f *fakeDigester) Digest(_ context.Context, doc extraction.Result) (Digest, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	for {
		old := atomic.LoadInt32(&f.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&f.maxSeen, old, n) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond) // create overlap so concurrency is observable
	atomic.AddInt32(&f.inFlight, -1)
	if err := f.errs[doc.Path]; err != nil {
		return Digest{}, err
	}
	return Digest{Path: doc.Path, Method: doc.Method, Text: "digest of " + doc.Path}, nil
}

// fakeReviewer records the digests it received and returns a fixed report.
type fakeReviewer struct {
	mu     sync.Mutex
	got    []Digest
	called bool
	report string
	err    error
}

func (f *fakeReviewer) Review(_ context.Context, digests []Digest) (string, error) {
	f.mu.Lock()
	f.called = true
	f.got = digests
	f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if f.report == "" {
		return "ОТЧЁТ", nil
	}
	return f.report, nil
}

func TestOrchestrator_EmptyPathsErrors(t *testing.T) {
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, &fakeReviewer{}, 2)
	if _, err := o.Review(context.Background(), nil); err == nil {
		t.Fatalf("want error for empty paths")
	}
}

func TestOrchestrator_HappyPathPreservesOrder(t *testing.T) {
	rev := &fakeReviewer{report: "ИТОГ"}
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, rev, 2)

	paths := []string{"/d/a.pdf", "/d/b.pdf", "/d/c.pdf"}
	report, err := o.Review(context.Background(), paths)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report != "ИТОГ" {
		t.Fatalf("want coordinator report, got %q", report)
	}
	if len(rev.got) != 3 {
		t.Fatalf("want 3 digests, got %d", len(rev.got))
	}
	for i, p := range paths {
		if rev.got[i].Path != p {
			t.Fatalf("order not preserved at %d: want %s, got %s", i, p, rev.got[i].Path)
		}
	}
}

// Падение извлечения/выжимки на одном документе не валит пачку: проблемный
// документ уходит координатору как «не прочитан» (пустой Text, путь сохранён),
// остальные — нормально.
func TestOrchestrator_WorkerFailureMarkedUnread(t *testing.T) {
	ext := &fakeExtractor{errs: map[string]error{"/d/bad.pdf": errors.New("ocr down")}}
	rev := &fakeReviewer{}
	o := NewOrchestrator(ext, &fakeDigester{}, rev, 3)

	paths := []string{"/d/ok1.pdf", "/d/bad.pdf", "/d/ok2.pdf"}
	if _, err := o.Review(context.Background(), paths); err != nil {
		t.Fatalf("batch must survive one failure: %v", err)
	}
	if len(rev.got) != 3 {
		t.Fatalf("want 3 digests passed to coordinator, got %d", len(rev.got))
	}
	if rev.got[1].Path != "/d/bad.pdf" || strings.TrimSpace(rev.got[1].Text) != "" {
		t.Fatalf("failed doc must be unread placeholder, got %+v", rev.got[1])
	}
	if rev.got[0].Text == "" || rev.got[2].Text == "" {
		t.Fatalf("healthy docs must still be digested")
	}
}

// Digester-ошибка (а не extractor) тоже даёт «не прочитан».
func TestOrchestrator_DigestFailureMarkedUnread(t *testing.T) {
	dig := &fakeDigester{errs: map[string]error{"/d/x.pdf": errors.New("llm down")}}
	rev := &fakeReviewer{}
	o := NewOrchestrator(&fakeExtractor{}, dig, rev, 2)
	if _, err := o.Review(context.Background(), []string{"/d/x.pdf", "/d/y.pdf"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if strings.TrimSpace(rev.got[0].Text) != "" {
		t.Fatalf("digest-failed doc must be unread, got %+v", rev.got[0])
	}
}

// Если НИ ОДИН документ не прочитан — координатор не зовётся, возвращается ошибка.
func TestOrchestrator_AllFailedDoesNotCallCoordinator(t *testing.T) {
	ext := &fakeExtractor{errs: map[string]error{
		"/d/a.pdf": errors.New("x"), "/d/b.pdf": errors.New("y"),
	}}
	rev := &fakeReviewer{}
	o := NewOrchestrator(ext, &fakeDigester{}, rev, 2)
	if _, err := o.Review(context.Background(), []string{"/d/a.pdf", "/d/b.pdf"}); err == nil {
		t.Fatalf("want error when nothing could be read")
	}
	if rev.called {
		t.Fatalf("coordinator must NOT be called when all docs failed")
	}
}

// Семафор реально ограничивает число одновременных воркеров.
func TestOrchestrator_BoundedConcurrency(t *testing.T) {
	dig := &fakeDigester{}
	o := NewOrchestrator(&fakeExtractor{}, dig, &fakeReviewer{}, 2)
	paths := []string{"/1", "/2", "/3", "/4", "/5"}
	if _, err := o.Review(context.Background(), paths); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if dig.maxSeen > 2 {
		t.Fatalf("concurrency exceeded limit: saw %d in flight, limit 2", dig.maxSeen)
	}
}

// fakeFacts returns scripted facts per path.
type fakeFacts struct {
	byPath map[string]Facts
	calls  int32
}

func (f *fakeFacts) Extract(_ context.Context, d Digest) Facts {
	atomic.AddInt32(&f.calls, 1)
	return f.byPath[d.Path]
}

// Фокус пользователя и автосверка уходят координатору первыми, документы
// сохраняют порядок после них.
func TestOrchestrator_FocusAndCollisionsPrepended(t *testing.T) {
	facts := &fakeFacts{byPath: map[string]Facts{
		"/d/техплан.xml": {DocType: "техплан", TEP: TEP{AreaTotalM2: fp(100)}},
		"/d/РнС.pdf":     {DocType: "разрешение на строительство", TEP: TEP{AreaTotalM2: fp(120)}},
	}}
	rev := &fakeReviewer{}
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, rev, 2).WithFacts(facts)

	_, err := o.ReviewRequest(context.Background(), ReviewRequest{
		Paths: []string{"/d/техплан.xml", "/d/РнС.pdf"},
		Focus: "сверь площадь",
	})
	if err != nil {
		t.Fatalf("ReviewRequest: %v", err)
	}
	if len(rev.got) != 4 {
		t.Fatalf("want focus + collisions + 2 docs, got %d: %+v", len(rev.got), rev.got)
	}
	if rev.got[0].Path != FocusDigestPath || rev.got[0].Text != "сверь площадь" {
		t.Fatalf("focus must come first: %+v", rev.got[0])
	}
	if rev.got[1].Path != CollisionsDigestPath || !strings.Contains(rev.got[1].Text, "🔴 Общая площадь") {
		t.Fatalf("collisions must follow focus: %+v", rev.got[1])
	}
	if rev.got[2].Path != "/d/техплан.xml" || rev.got[3].Path != "/d/РнС.pdf" {
		t.Fatalf("document order not preserved: %+v", rev.got[2:])
	}
	if rev.got[2].Facts.DocType != "техплан" {
		t.Fatalf("facts must be attached to the digest: %+v", rev.got[2])
	}
	if facts.calls != 2 {
		t.Fatalf("facts pass must run once per read document, got %d", facts.calls)
	}
}

// Без фокуса и без фактов координатор получает ровно документы, как раньше.
func TestOrchestrator_NoFactsNoPseudoDocs(t *testing.T) {
	rev := &fakeReviewer{}
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, rev, 2)
	if _, err := o.Review(context.Background(), []string{"/d/a.pdf", "/d/b.pdf"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(rev.got) != 2 || rev.got[0].Path != "/d/a.pdf" {
		t.Fatalf("plain review must pass documents only: %+v", rev.got)
	}
}

// Факты не извлекаются у непрочитанных документов.
func TestOrchestrator_FactsSkipUnread(t *testing.T) {
	facts := &fakeFacts{}
	ext := &fakeExtractor{errs: map[string]error{"/d/bad.pdf": errors.New("ocr down")}}
	o := NewOrchestrator(ext, &fakeDigester{}, &fakeReviewer{}, 2).WithFacts(facts)
	if _, err := o.Review(context.Background(), []string{"/d/ok.pdf", "/d/bad.pdf"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if facts.calls != 1 {
		t.Fatalf("facts must run only for read documents, got %d calls", facts.calls)
	}
}

// Нулевая конкуррентность не должна порождать дедлок (семафор размера 0).
func TestOrchestrator_ZeroConcurrencyDoesNotDeadlock(t *testing.T) {
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, &fakeReviewer{}, 0)
	done := make(chan struct{})
	go func() {
		_, _ = o.Review(context.Background(), []string{"/a", "/b", "/c"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("zero concurrency deadlocked — default must apply")
	}
}
