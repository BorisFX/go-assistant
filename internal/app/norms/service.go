package norms

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const (
	defaultEmbedConcurrency = 4
	// perRefLimit bounds what one citation pulls in: "ст. 26" alone would
	// otherwise return every one of its sixty points.
	perRefLimit = 4
	// scopeChunks is how much of a document a bare mention ("СП 4.13130")
	// contributes: its opening, where the scope of application is stated.
	scopeChunks = 2
	// queryChars caps the text embedded for semantic search.
	queryCharsPerText = 2000
	queryCharsTotal   = 8000
)

// Stats summarizes one ingest run.
type Stats struct {
	Documents int
	Skipped   int
	Chunks    int
}

// Service is the corpus: ingest files, look up units, search, and assemble
// excerpts for a review.
type Service struct {
	repo        Repository
	embedder    output.EmbeddingProvider
	concurrency int
}

func NewService(repo Repository, embedder output.EmbeddingProvider, concurrency int) *Service {
	if concurrency <= 0 {
		concurrency = defaultEmbedConcurrency
	}
	return &Service{repo: repo, embedder: embedder, concurrency: concurrency}
}

// Ingest loads dir into the corpus. A document whose file hash is unchanged
// is skipped, so the command is safe to run on every deploy.
func (s *Service) Ingest(ctx context.Context, dir string) (Stats, error) {
	sources, err := LoadCorpus(ctx, dir)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, src := range sources {
		stored, err := s.repo.DocumentHash(ctx, src.Document.Code)
		if err != nil {
			return st, fmt.Errorf("norms: hash of %s: %w", src.Document.Code, err)
		}
		if stored == src.Document.FileHash {
			st.Skipped++
			continue
		}
		chunks := Split(src.Text)
		for i := range chunks {
			chunks[i].DocCode = src.Document.Code
		}
		start := time.Now()
		if err := s.embedAll(ctx, chunks); err != nil {
			return st, fmt.Errorf("norms: embed %s: %w", src.Document.Code, err)
		}
		doc := src.Document
		doc.IngestedAt = time.Now()
		if err := s.repo.UpsertDocument(ctx, doc); err != nil {
			return st, fmt.Errorf("norms: store %s: %w", doc.Code, err)
		}
		if err := s.repo.ReplaceChunks(ctx, doc.Code, chunks); err != nil {
			return st, fmt.Errorf("norms: store chunks of %s: %w", doc.Code, err)
		}
		st.Documents++
		st.Chunks += len(chunks)
		slog.Info("norms: document ingested", "code", doc.Code, "edition", doc.Edition,
			"chunks", len(chunks), "ms", time.Since(start).Milliseconds())
	}
	return st, nil
}

// embedAll fills Embedding on every chunk with bounded concurrency. One
// failed chunk fails the document: a unit without a vector is invisible to
// search, and a silently half-indexed law is worse than none.
func (s *Service) embedAll(ctx context.Context, chunks []Chunk) error {
	sem := make(chan struct{}, s.concurrency)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			emb, err := s.embedder.Embed(ctx, chunks[i].Ref+"\n"+chunks[i].Text)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("chunk %d (%s): %w", i, chunks[i].Ref, err)
				return
			}
			chunks[i].Embedding = emb
		}(i)
	}
	wg.Wait()
	return firstErr
}

// Documents lists what is loaded.
func (s *Service) Documents(ctx context.Context) ([]Document, error) {
	return s.repo.ListDocuments(ctx)
}

// Lookup returns the chunks of one unit. docCode may be a prefix ("СП
// 4.13130" for "СП 4.13130.2013", "ГрК" for "ГрК РФ"); ref is any spelling
// NormalizeRef accepts. An empty ref returns the document's opening.
func (s *Service) Lookup(ctx context.Context, docCode, ref string) ([]Chunk, error) {
	docs, err := s.repo.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	code, ok := resolveDoc(docs, docCode)
	if !ok {
		return nil, nil
	}
	limit := perRefLimit
	if strings.TrimSpace(ref) == "" {
		limit = scopeChunks
	}
	return s.repo.FindByRef(ctx, code, NormalizeRef(ref), limit)
}

// Search finds units semantically close to query.
func (s *Service) Search(ctx context.Context, query string, k int) ([]Chunk, error) {
	if k <= 0 {
		k = 5
	}
	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("norms: embed query: %w", err)
	}
	return s.repo.SearchSimilar(ctx, emb, k)
}

// Excerpts assembles the normative text a review may cite: every unit the
// texts reference explicitly, then semantic neighbours of the texts, at most
// limit chunks. Returns "" when the corpus is empty. A failed embedding
// degrades to exact hits only — a citation lookup must not depend on the
// embedding endpoint being up.
func (s *Service) Excerpts(ctx context.Context, texts []string, limit int) (string, error) {
	docs, err := s.repo.ListDocuments(ctx)
	if err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "", nil
	}
	if limit <= 0 {
		limit = 12
	}
	editions := make(map[string]string, len(docs))
	for _, d := range docs {
		editions[d.Code] = d.Edition
	}

	var (
		hits []Chunk
		seen = map[string]bool{}
	)
	add := func(cs []Chunk) {
		for _, c := range cs {
			if seen[c.Key()] || len(hits) >= limit {
				continue
			}
			seen[c.Key()] = true
			hits = append(hits, c)
		}
	}

	joined := strings.Join(texts, "\n")
	for _, q := range ExtractRefs(joined) {
		if len(hits) >= limit {
			break
		}
		code, ok := resolveDoc(docs, q.DocCode)
		if !ok {
			continue
		}
		lim := perRefLimit
		if q.Ref == "" {
			lim = scopeChunks
		}
		cs, err := s.repo.FindByRef(ctx, code, q.Ref, lim)
		if err != nil {
			return "", err
		}
		add(cs)
	}

	if len(hits) < limit {
		query := buildQuery(texts)
		if query != "" {
			emb, err := s.embedder.Embed(ctx, query)
			if err != nil {
				slog.Warn("norms: semantic search skipped, embedding failed", "error", err)
			} else {
				// Ask for the full limit: the nearest neighbours often repeat
				// the exact hits, and add() drops those duplicates.
				cs, err := s.repo.SearchSimilar(ctx, emb, limit)
				if err != nil {
					return "", err
				}
				add(cs)
			}
		}
	}
	return FormatExcerpts(hits, editions), nil
}

// FormatExcerpts renders chunks as citable blocks:
// [СП 4.13130.2013, п. 4.3, ред. 2020] «текст».
func FormatExcerpts(chunks []Chunk, editions map[string]string) string {
	var b strings.Builder
	for i, c := range chunks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[")
		b.WriteString(c.DocCode)
		if c.Ref != "" {
			b.WriteString(", ")
			b.WriteString(c.Ref)
		}
		if ed := editions[c.DocCode]; ed != "" {
			b.WriteString(", ред. ")
			b.WriteString(ed)
		}
		b.WriteString("] «")
		b.WriteString(strings.TrimSpace(c.Text))
		b.WriteString("»")
	}
	return b.String()
}

// resolveDoc matches a cited code against loaded documents by normalized
// prefix. The shortest matching code wins, so "СП 4" cannot silently pick a
// longer neighbour over an exact one.
func resolveDoc(docs []Document, cited string) (string, bool) {
	want := NormalizeDocCode(cited)
	if want == "" {
		return "", false
	}
	best := ""
	for _, d := range docs {
		have := NormalizeDocCode(d.Code)
		if have == want {
			return d.Code, true
		}
		if strings.HasPrefix(have, want) || strings.HasPrefix(want, have) {
			if best == "" || len(d.Code) < len(best) {
				best = d.Code
			}
		}
	}
	return best, best != ""
}

// buildQuery takes the head of each text, capped overall, as the semantic
// probe. Heads carry the summary lines a digest starts with.
func buildQuery(texts []string) string {
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > queryCharsPerText {
			t = cutRunes(t, queryCharsPerText)
		}
		if b.Len()+len(t) > queryCharsTotal {
			t = cutRunes(t, queryCharsTotal-b.Len())
			if t == "" {
				break
			}
		}
		b.WriteString(t)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func cutRunes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
