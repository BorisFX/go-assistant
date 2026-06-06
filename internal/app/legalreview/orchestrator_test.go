package legalreview

import (
	"context"
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
