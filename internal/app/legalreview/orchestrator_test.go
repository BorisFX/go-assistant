package legalreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	partial  map[string]bool
	calls    int32
	inFlight int32
	maxSeen  int32
}

func (f *fakeDigester) Digest(_ context.Context, doc extraction.Result) (Digest, error) {
	atomic.AddInt32(&f.calls, 1)
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
	return Digest{Path: doc.Path, Method: doc.Method, Text: "digest of " + doc.Path, Partial: f.partial[doc.Path]}, nil
}

// fakeReviewer records the digests it received and returns a fixed report.
type fakeReviewer struct {
	mu       sync.Mutex
	got      []Digest
	called   bool
	report   string
	evidence []string
	err      error
}

func (f *fakeReviewer) Review(_ context.Context, digests []Digest) (Conclusion, error) {
	f.mu.Lock()
	f.called = true
	f.got = digests
	f.mu.Unlock()
	if f.err != nil {
		return Conclusion{}, f.err
	}
	if f.report == "" {
		return Conclusion{Report: "ОТЧЁТ"}, nil
	}
	return Conclusion{Report: f.report, Evidence: f.evidence}, nil
}

// docsOnly drops the program-built blocks (manifest, cross-check, focus,
// context) so a test can assert about the documents themselves.
func docsOnly(got []Digest) []Digest {
	pseudo := map[string]bool{
		ManifestDigestPath:   true,
		CollisionsDigestPath: true,
		FocusDigestPath:      true,
		ContextDigestPath:    true,
	}
	var out []Digest
	for _, d := range got {
		if !pseudo[d.Path] {
			out = append(out, d)
		}
	}
	return out
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
	docs := docsOnly(rev.got)
	if len(docs) != 3 {
		t.Fatalf("want 3 digests, got %d", len(docs))
	}
	for i, p := range paths {
		if docs[i].Path != p {
			t.Fatalf("order not preserved at %d: want %s, got %s", i, p, docs[i].Path)
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
	docs := docsOnly(rev.got)
	if len(docs) != 3 {
		t.Fatalf("want 3 digests passed to coordinator, got %d", len(docs))
	}
	if docs[1].Path != "/d/bad.pdf" || strings.TrimSpace(docs[1].Text) != "" {
		t.Fatalf("failed doc must be unread placeholder, got %+v", docs[1])
	}
	if docs[0].Text == "" || docs[2].Text == "" {
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
	if docs := docsOnly(rev.got); strings.TrimSpace(docs[0].Text) != "" {
		t.Fatalf("digest-failed doc must be unread, got %+v", docs[0])
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
	if len(rev.got) != 5 {
		t.Fatalf("want focus + manifest + collisions + 2 docs, got %d: %+v", len(rev.got), rev.got)
	}
	if rev.got[0].Path != FocusDigestPath || rev.got[0].Text != "сверь площадь" {
		t.Fatalf("focus must come first: %+v", rev.got[0])
	}
	if rev.got[1].Path != ManifestDigestPath {
		t.Fatalf("manifest must follow focus: %+v", rev.got[1])
	}
	if rev.got[2].Path != CollisionsDigestPath || !strings.Contains(rev.got[2].Text, "🔴 Общая площадь") {
		t.Fatalf("collisions must follow the manifest: %+v", rev.got[2])
	}
	docs := docsOnly(rev.got)
	if docs[0].Path != "/d/техплан.xml" || docs[1].Path != "/d/РнС.pdf" {
		t.Fatalf("document order not preserved: %+v", docs)
	}
	if docs[0].Facts.DocType != "техплан" {
		t.Fatalf("facts must be attached to the digest: %+v", docs[0])
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
	if docs := docsOnly(rev.got); len(docs) != 2 || docs[0].Path != "/d/a.pdf" {
		t.Fatalf("plain review must pass documents only: %+v", docs)
	}
	// The manifest is always there; the optional blocks are not.
	for _, d := range rev.got {
		if d.Path == FocusDigestPath || d.Path == ContextDigestPath || d.Path == CollisionsDigestPath {
			t.Fatalf("unexpected block %q without focus, context or facts", d.Path)
		}
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

func TestOrchestrator_ContextFollowsFocus(t *testing.T) {
	rev := &fakeReviewer{}
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, rev, 2)

	_, err := o.ReviewRequest(context.Background(), ReviewRequest{
		Paths:   []string{"/d/РнС.pdf"},
		Focus:   "сверь площадь",
		Context: "— Пользователь: вчера пришла приостановка",
	})
	if err != nil {
		t.Fatalf("ReviewRequest: %v", err)
	}
	if len(rev.got) != 4 {
		t.Fatalf("want focus + context + manifest + doc, got %d: %+v", len(rev.got), rev.got)
	}
	if rev.got[0].Path != FocusDigestPath {
		t.Fatalf("focus must come first: %+v", rev.got[0])
	}
	if rev.got[1].Path != ContextDigestPath || rev.got[1].Method != "user" || !strings.Contains(rev.got[1].Text, "приостановка") {
		t.Fatalf("context must follow focus: %+v", rev.got[1])
	}
	if docs := docsOnly(rev.got); len(docs) != 1 || docs[0].Path != "/d/РнС.pdf" {
		t.Fatalf("document must come last: %+v", docs)
	}
}

// A coordinator failure after twenty minutes of digesting must not cost those
// twenty minutes again: digests are cached by file content, and a rerun over
// the same files only pays for the coordinator.
func TestOrchestrator_DigestCacheSkipsRepeatWork(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "РнС.pdf")
	if err := os.WriteFile(doc, []byte("permit bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	dg := &fakeDigester{}
	facts := &fakeFacts{byPath: map[string]Facts{doc: {DocType: "разрешение на строительство"}}}
	o := NewOrchestrator(&fakeExtractor{}, dg, &fakeReviewer{}, 2).WithFacts(facts).WithCache(filepath.Join(dir, "cache"), "v1")

	if _, err := o.Review(context.Background(), []string{doc}); err != nil {
		t.Fatalf("first review: %v", err)
	}
	if _, err := o.Review(context.Background(), []string{doc}); err != nil {
		t.Fatalf("second review: %v", err)
	}
	if got := atomic.LoadInt32(&dg.calls); got != 1 {
		t.Fatalf("digester called %d times, want 1 (second run served from cache)", got)
	}
	if facts.calls != 1 {
		t.Fatalf("facts extracted %d times, want 1", facts.calls)
	}
	rev := &fakeReviewer{}
	o2 := NewOrchestrator(&fakeExtractor{}, dg, rev, 2).WithFacts(facts).WithCache(filepath.Join(dir, "cache"), "v1")
	if _, err := o2.Review(context.Background(), []string{doc}); err != nil {
		t.Fatal(err)
	}
	if cached := docsOnly(rev.got)[0]; cached.Facts.DocType != "разрешение на строительство" || !strings.Contains(cached.Text, "digest of") {
		t.Fatalf("cached digest must carry text and facts: %+v", cached)
	}

	// Changed bytes or a different key salt miss the cache.
	if err := os.WriteFile(doc, []byte("permit bytes v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Review(context.Background(), []string{doc}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&dg.calls); got != 2 {
		t.Fatalf("digester called %d times after content change, want 2", got)
	}
}

func TestOrchestrator_PartialDigestIsNotCached(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "ГПЗУ.pdf")
	if err := os.WriteFile(doc, []byte("gpzu"), 0o644); err != nil {
		t.Fatal(err)
	}
	dg := &fakeDigester{partial: map[string]bool{doc: true}}
	o := NewOrchestrator(&fakeExtractor{}, dg, &fakeReviewer{}, 2).WithCache(filepath.Join(dir, "cache"), "v1")
	for i := 0; i < 2; i++ {
		if _, err := o.Review(context.Background(), []string{doc}); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&dg.calls); got != 2 {
		t.Fatalf("partial digest must be redone on rerun, digester calls = %d", got)
	}
}

// A number the coordinator wrote but no source contains is surfaced, not hidden.
func TestOrchestrator_VerifiesReportNumbers(t *testing.T) {
	rev := &fakeReviewer{report: "Площадь участка 29 439 м², в томе ОДИ — 28 600 м².",
		evidence: []string{"норма без чисел"}}
	o := NewOrchestrator(&fakeExtractor{}, &fakeDigester{}, rev, 2).WithFacts(&fakeFacts{byPath: map[string]Facts{
		"/d/а.pdf": {DocType: "техплан", TEP: TEP{LandAreaM2: fp(29439)}},
		"/d/б.pdf": {DocType: "разрешение", TEP: TEP{LandAreaM2: fp(29439)}},
	}})

	res, err := o.ReviewRequest(context.Background(), ReviewRequest{Paths: []string{"/d/а.pdf", "/d/б.pdf"}})
	if err != nil {
		t.Fatalf("ReviewRequest: %v", err)
	}
	if len(res.Unverified) != 1 || res.Unverified[0].Value != "28600" {
		t.Fatalf("want 28600 flagged, got %+v", res.Unverified)
	}
	if !strings.Contains(res.CrossCheck, "АВТОСВЕРКА") {
		t.Fatalf("cross-check must come back for the report: %q", res.CrossCheck)
	}
}

// The reduce pass merges forty digests into three blocks, and files that fall
// out of the squeeze look «не прочитан» to the coordinator. The authoritative
// roster of what was read is built in Go and handed over as its own block.
func TestOrchestrator_ManifestListsEveryDocument(t *testing.T) {
	rev := &fakeReviewer{}
	o := NewOrchestrator(&fakeExtractor{errs: map[string]error{"/d/битый.pdf": errors.New("boom")}}, &fakeDigester{}, rev, 2)

	if _, err := o.ReviewRequest(context.Background(), ReviewRequest{Paths: []string{"/d/а.pdf", "/d/битый.pdf"}}); err != nil {
		t.Fatalf("ReviewRequest: %v", err)
	}
	var manifest string
	for _, d := range rev.got {
		if d.Path == ManifestDigestPath {
			manifest = d.Text
		}
	}
	if manifest == "" {
		t.Fatalf("no manifest among %d blocks", len(rev.got))
	}
	if !strings.Contains(manifest, "а.pdf") || !strings.Contains(manifest, "прочитан") {
		t.Errorf("read document missing from the manifest:\n%s", manifest)
	}
	if !strings.Contains(manifest, "битый.pdf") || !strings.Contains(manifest, "НЕ прочитан") {
		t.Errorf("unread document must be marked as such:\n%s", manifest)
	}
}
