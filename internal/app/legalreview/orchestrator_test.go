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
