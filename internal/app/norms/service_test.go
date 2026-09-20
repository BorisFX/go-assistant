package norms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRepo is an in-memory Repository.
type fakeRepo struct {
	mu     sync.Mutex
	docs   map[string]Document
	chunks map[string][]Chunk
	simRes []Chunk
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{docs: map[string]Document{}, chunks: map[string][]Chunk{}}
}

func (f *fakeRepo) UpsertDocument(_ context.Context, d Document) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[d.Code] = d
	return nil
}

func (f *fakeRepo) ReplaceChunks(_ context.Context, code string, cs []Chunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks[code] = cs
	return nil
}

func (f *fakeRepo) FindByRef(_ context.Context, code, prefix string, limit int) ([]Chunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Chunk
	for _, c := range f.chunks[code] {
		n := NormalizeRef(c.Ref)
		if prefix == "" || n == prefix || strings.HasPrefix(n, prefix+" ") {
			out = append(out, c)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) SearchSimilar(_ context.Context, _ []float32, limit int) ([]Chunk, error) {
	if len(f.simRes) > limit {
		return f.simRes[:limit], nil
	}
	return f.simRes, nil
}

func (f *fakeRepo) ListDocuments(_ context.Context) ([]Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Document
	for _, d := range f.docs {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeRepo) DocumentHash(_ context.Context, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docs[code].FileHash, nil
}

type fakeEmbedder struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (e *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	return []float32{float32(len(text))}, nil
}

func writeCorpus(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIngest_IndexesAndSkipsUnchanged(t *testing.T) {
	dir := writeCorpus(t, map[string]string{
		"218-fz.md":     law218,
		"manifest.yaml": "218-fz.md:\n  code: 218-ФЗ\n  title: О госрегистрации недвижимости\n  edition: 2024-08-08\n  source: consultant.ru\n",
	})
	repo, emb := newFakeRepo(), &fakeEmbedder{}
	svc := NewService(repo, emb, 2)

	st, err := svc.Ingest(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Documents != 1 || st.Chunks == 0 || st.Skipped != 0 {
		t.Fatalf("stats %+v", st)
	}
	if repo.docs["218-ФЗ"].Edition != "2024-08-08" {
		t.Fatalf("manifest metadata not stored: %+v", repo.docs)
	}
	for _, c := range repo.chunks["218-ФЗ"] {
		if len(c.Embedding) == 0 || c.DocCode != "218-ФЗ" {
			t.Fatalf("chunk not embedded or unlabeled: %+v", c)
		}
	}
	firstCalls := emb.calls

	st, err = svc.Ingest(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 1 || st.Documents != 0 || emb.calls != firstCalls {
		t.Fatalf("second run should skip unchanged file: %+v calls=%d", st, emb.calls)
	}
}

func TestIngest_EmbedFailureFailsDocument(t *testing.T) {
	dir := writeCorpus(t, map[string]string{"sp4.txt": sp4})
	repo := newFakeRepo()
	svc := NewService(repo, &fakeEmbedder{err: errors.New("quota")}, 2)
	if _, err := svc.Ingest(context.Background(), dir); err == nil {
		t.Fatal("expected error")
	}
	if len(repo.chunks) != 0 {
		t.Fatal("half-embedded document must not be stored")
	}
}

func TestIngest_CodeFromFileNameWithoutManifest(t *testing.T) {
	dir := writeCorpus(t, map[string]string{"СП 4.13130.2013.txt": sp4})
	repo := newFakeRepo()
	if _, err := NewService(repo, &fakeEmbedder{}, 1).Ingest(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.docs["СП 4.13130.2013"]; !ok {
		t.Fatalf("docs: %+v", repo.docs)
	}
}

func seeded(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	dir := writeCorpus(t, map[string]string{
		"218-ФЗ.md":          law218,
		"СП 4.13130.2013.md": sp4,
		"manifest.yaml":      "СП 4.13130.2013.md:\n  edition: 2020\n",
	})
	repo := newFakeRepo()
	svc := NewService(repo, &fakeEmbedder{}, 2)
	if _, err := svc.Ingest(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return svc, repo
}

func TestLookup_ExactAndPrefixDocCode(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()

	cs, err := svc.Lookup(ctx, "218-ФЗ", "статья 26 часть 1 пункт 7")
	if err != nil || len(cs) != 1 || !strings.Contains(cs[0].Text, "форма и (или) содержание") {
		t.Fatalf("lookup 218: %v %+v", err, cs)
	}
	cs, err = svc.Lookup(ctx, "СП 4.13130", "п.4.3")
	if err != nil || len(cs) < 1 || cs[0].Ref != "п. 4.3" {
		t.Fatalf("lookup by doc prefix: %v %+v", err, cs)
	}
	// Prefix on the ref: "ст. 26 ч. 1" returns the part and its points, capped.
	cs, _ = svc.Lookup(ctx, "218-ФЗ", "ст. 26 ч. 1")
	if len(cs) == 0 || len(cs) > perRefLimit || cs[0].Ref != "ст. 26 ч. 1" {
		t.Fatalf("prefix lookup: %+v", refsOf(cs))
	}
	// Unknown document → nothing, no error.
	cs, err = svc.Lookup(ctx, "СНиП 2.01.01", "п. 1")
	if err != nil || len(cs) != 0 {
		t.Fatalf("unknown doc: %v %+v", err, cs)
	}
	// Empty ref → the document's opening.
	cs, _ = svc.Lookup(ctx, "СП 4.13130.2013", "")
	if len(cs) != scopeChunks || cs[0].Ref != preambleRef {
		t.Fatalf("opening: %+v", refsOf(cs))
	}
}

func TestExcerpts_ExactHitsThenSemanticFormatted(t *testing.T) {
	svc, repo := seeded(t)
	repo.simRes = []Chunk{{DocCode: "218-ФЗ", Ref: "ст. 27", Ord: 99, Text: "отказывается"}}

	out, err := svc.Excerpts(context.Background(), []string{
		"Замечание регистратора: нарушен п. 7 ч. 1 ст. 26 218-ФЗ.",
		"Расстояние не соответствует п. 4.3 СП 4.13130.2013.",
	}, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[218-ФЗ, ст. 26 ч. 1 п. 7] «",
		"[СП 4.13130.2013, п. 4.3, ред. 2020] «4.3 Противопожарные расстояния",
		"[218-ФЗ, ст. 27] «отказывается»",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "ст. 26 ч. 1 п. 7") > strings.Index(out, "ст. 27") {
		t.Fatal("exact hits must precede semantic ones")
	}
}

func TestExcerpts_LimitAndDedupe(t *testing.T) {
	svc, repo := seeded(t)
	// Semantic result duplicates an exact hit: must appear once.
	exact, _ := svc.Lookup(context.Background(), "218-ФЗ", "ст. 26 ч. 1 п. 7")
	repo.simRes = []Chunk{exact[0], {DocCode: "218-ФЗ", Ref: "ст. 27", Text: "x"}}

	out, err := svc.Excerpts(context.Background(), []string{"п. 7 ч. 1 ст. 26 218-ФЗ"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "] «"); n != 2 {
		t.Fatalf("want exactly 2 blocks, got %d:\n%s", n, out)
	}
	if strings.Count(out, "ст. 26 ч. 1 п. 7") != 1 {
		t.Fatalf("duplicate hit:\n%s", out)
	}
}

func TestExcerpts_EmptyCorpusReturnsEmpty(t *testing.T) {
	svc := NewService(newFakeRepo(), &fakeEmbedder{}, 1)
	out, err := svc.Excerpts(context.Background(), []string{"ст. 26 218-ФЗ"}, 5)
	if err != nil || out != "" {
		t.Fatalf("want empty, got %q err=%v", out, err)
	}
}

func TestExcerpts_EmbeddingFailureKeepsExactHits(t *testing.T) {
	svc, repo := seeded(t)
	svc.embedder = &fakeEmbedder{err: errors.New("down")}
	repo.simRes = []Chunk{{DocCode: "218-ФЗ", Ref: "ст. 27", Text: "x"}}

	out, err := svc.Excerpts(context.Background(), []string{"п. 7 ч. 1 ст. 26 218-ФЗ"}, 5)
	if err != nil || !strings.Contains(out, "ст. 26 ч. 1 п. 7") || strings.Contains(out, "ст. 27") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestResolveDoc_ShortestPrefixWins(t *testing.T) {
	docs := []Document{{Code: "СП 4.13130.2013"}, {Code: "СП 42.13330.2016"}, {Code: "ГрК РФ"}}
	if code, ok := resolveDoc(docs, "СП 4.13130"); !ok || code != "СП 4.13130.2013" {
		t.Fatal(code, ok)
	}
	if code, ok := resolveDoc(docs, "ГрК"); !ok || code != "ГрК РФ" {
		t.Fatal(code, ok)
	}
	if _, ok := resolveDoc(docs, ""); ok {
		t.Fatal("empty must not resolve")
	}
}

func TestBuildQuery_Caps(t *testing.T) {
	big := strings.Repeat("слово ", 3000)
	q := buildQuery([]string{big, big, big, big, big, big})
	if len(q) > queryCharsTotal+1 {
		t.Fatalf("query over cap: %d", len(q))
	}
}
