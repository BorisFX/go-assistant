# Memory / RAG Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the bot's long-term memory work again — reliable daily summaries with backfill, real-time fact extraction, a relevance threshold on retrieval, and fact deduplication.

**Architecture:** Go 1.22, Hexagonal (ports & adapters). Memory lives in PostgreSQL + pgvector, accessed through `output.MemoryRepository`. The `memory.Service` builds RAG context and stores facts; `memory.Summarizer` runs daily; a new `memory.Extractor` pulls facts in real time. Chat flow lives in `chat.Service`.

**Tech Stack:** Go, PostgreSQL/pgvector, OpenRouter (LLM + embeddings), standard `testing` package (no testify — hand-written mocks, see `internal/app/memory/service_test.go`).

**Spec:** `docs/superpowers/specs/2026-05-29-memory-rag-fixes-design.md`

---

## File Structure

- `pkg/config/config.go` — extend `MemoryConfig` + `setDefaults()` with 4 new fields.
- `configs/config.example.yaml` — document the new `memory:` keys.
- `internal/port/output/memory_repo.go` — add `ScoredMemory` type + `SearchSimilarScored` to the interface.
- `internal/adapter/driven/postgres/memory_repo.go` — implement `SearchSimilarScored`.
- `internal/app/memory/service.go` — add `ServiceConfig`, dedup in `StoreFact`, threshold + top-k in `BuildContext`.
- `internal/app/memory/service_test.go` — update mock for the new interface method; add dedup + threshold tests.
- `internal/app/memory/extractor.go` — NEW: real-time fact extractor + `parseFacts` helper.
- `internal/app/memory/extractor_test.go` — NEW: `parseFacts` tests.
- `internal/app/memory/summarizer.go` — `summarizeDay`, `missingDays`, `catchUp`, new `Run`.
- `internal/app/memory/summarizer_test.go` — NEW: `missingDays` test.
- `internal/app/chat/service.go` — per-conversation counter + trigger extractor; new `NewService` param.
- `cmd/assistant/main.go` — build extractor, pass config into `memory.NewService` and `chat.NewService`.

---

## Task 1: Config fields and defaults

**Files:**
- Modify: `pkg/config/config.go` (MemoryConfig struct ~line 93; `setDefaults()` ~the `c.Memory.*` block)
- Modify: `configs/config.example.yaml`

- [ ] **Step 1: Add fields to `MemoryConfig`**

In `pkg/config/config.go`, replace the existing `MemoryConfig` struct with:

```go
type MemoryConfig struct {
	ShortTermLimit       int           `yaml:"short_term_limit"`
	WorkingMemoryResults int           `yaml:"working_memory_results"`
	MaxContextTokens     int           `yaml:"max_context_tokens"`
	RetentionDays        int           `yaml:"retention_days"`
	SummarizeInterval    time.Duration `yaml:"summarize_interval"`

	FactExtractionInterval int     `yaml:"fact_extraction_interval"`
	ExtractionModel        string  `yaml:"extraction_model"`
	SimilarityThreshold    float64 `yaml:"similarity_threshold"`
	DedupThreshold         float64 `yaml:"dedup_threshold"`
}
```

- [ ] **Step 2: Add defaults**

In `setDefaults()`, immediately after the existing `if c.Memory.SummarizeInterval == 0 { ... }` block, add:

```go
	if c.Memory.FactExtractionInterval == 0 {
		c.Memory.FactExtractionInterval = 6
	}
	if c.Memory.ExtractionModel == "" {
		c.Memory.ExtractionModel = "deepseek/deepseek-v4-flash"
	}
	if c.Memory.SimilarityThreshold == 0 {
		c.Memory.SimilarityThreshold = 0.45
	}
	if c.Memory.DedupThreshold == 0 {
		c.Memory.DedupThreshold = 0.15
	}
```

- [ ] **Step 3: Document in example config**

Append to `configs/config.example.yaml` (under a `memory:` key; create the key if absent):

```yaml
memory:
  fact_extraction_interval: 6          # extract facts every N messages
  extraction_model: "deepseek/deepseek-v4-flash"
  similarity_threshold: 0.45           # max cosine distance for RAG matches
  dedup_threshold: 0.15                # skip facts nearer than this to an existing one
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add pkg/config/config.go configs/config.example.yaml
git commit -m "feat(config): add memory extraction and threshold settings"
```

---

## Task 2: Repository scored similarity search

**Files:**
- Modify: `internal/port/output/memory_repo.go`
- Modify: `internal/adapter/driven/postgres/memory_repo.go`

`SearchSimilarScored` returns matches with their cosine distance so callers can apply a threshold. `memType == ""` means "any type".

- [ ] **Step 1: Extend the port interface**

In `internal/port/output/memory_repo.go`, add the `ScoredMemory` type and a method to the `MemoryRepository` interface:

```go
type ScoredMemory struct {
	Memory   *entity.Memory
	Distance float64
}
```

Add this line inside the `MemoryRepository` interface, right after the existing `SearchSimilar(...)` line:

```go
	SearchSimilarScored(ctx context.Context, embedding []float32, memType entity.MemoryType, limit int) ([]ScoredMemory, error)
```

- [ ] **Step 2: Implement in postgres adapter**

In `internal/adapter/driven/postgres/memory_repo.go`, add (after the existing `SearchSimilar` method):

```go
func (r *MemoryRepo) SearchSimilarScored(ctx context.Context, embedding []float32, memType entity.MemoryType, limit int) ([]output.ScoredMemory, error) {
	embStr := formatVector(embedding)

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at, embedding <=> $1::vector AS distance
		 FROM memories
		 WHERE embedding IS NOT NULL AND ($2 = '' OR type = $2)
		 ORDER BY embedding <=> $1::vector
		 LIMIT $3`,
		embStr, string(memType), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []output.ScoredMemory
	for rows.Next() {
		var m entity.Memory
		var expiresAt sql.NullTime
		var distance float64
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, pq.Array(&m.Tags), &m.Source, &m.CreatedAt, &expiresAt, &distance); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			m.ExpiresAt = &expiresAt.Time
		}
		mem := m
		results = append(results, output.ScoredMemory{Memory: &mem, Distance: distance})
	}
	return results, rows.Err()
}
```

- [ ] **Step 3: Add the import**

The postgres file must import the output package. At the top of `internal/adapter/driven/postgres/memory_repo.go`, add to the import block:

```go
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: it will FAIL with an error that `*mockMemoryRepo` does not implement `MemoryRepository` (missing `SearchSimilarScored`). That is expected and fixed in Task 3, Step 1. The non-test build target must still pass:

Run: `go build ./internal/adapter/... ./internal/port/... ./pkg/...`
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add internal/port/output/memory_repo.go internal/adapter/driven/postgres/memory_repo.go
git commit -m "feat(memory): add scored similarity search to repository"
```

---

## Task 3: Service config, dedup, and retrieval threshold

**Files:**
- Modify: `internal/app/memory/service.go`
- Test: `internal/app/memory/service_test.go`

- [ ] **Step 1: Update the test mock for the new interface method**

In `internal/app/memory/service_test.go`, add a field to track scored results and implement the new method. Change the `mockMemoryRepo` struct to:

```go
type mockMemoryRepo struct {
	stored   []*entity.Memory
	searched []*entity.Memory
	scored   []output.ScoredMemory
}
```

Add this method to the mock (alongside the other mock methods):

```go
func (m *mockMemoryRepo) SearchSimilarScored(ctx context.Context, embedding []float32, memType entity.MemoryType, limit int) ([]output.ScoredMemory, error) {
	return m.scored, nil
}
```

Add the import to the test file's import block:

```go
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
```

- [ ] **Step 2: Write the failing dedup test**

Add to `internal/app/memory/service_test.go`:

```go
func TestMemoryService_StoreFact_SkipsDuplicate(t *testing.T) {
	repo := &mockMemoryRepo{
		scored: []output.ScoredMemory{
			{Memory: &entity.Memory{Type: entity.MemoryFact, Content: "user is a Go developer"}, Distance: 0.05},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45,
		DedupThreshold:      0.15,
		TopK:                5,
		FactLimit:           10,
		SummaryDays:         7,
	})

	if err := svc.StoreFact(context.Background(), "user is a Golang developer", "realtime", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.stored) != 0 {
		t.Fatalf("expected duplicate fact to be skipped, but %d were stored", len(repo.stored))
	}
}

func TestMemoryService_StoreFact_StoresWhenNotDuplicate(t *testing.T) {
	repo := &mockMemoryRepo{
		scored: []output.ScoredMemory{
			{Memory: &entity.Memory{Type: entity.MemoryFact, Content: "user lives in Phnom Penh"}, Distance: 0.6},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45,
		DedupThreshold:      0.15,
		TopK:                5,
		FactLimit:           10,
		SummaryDays:         7,
	})

	if err := svc.StoreFact(context.Background(), "user is a Go developer", "realtime", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.stored) != 1 {
		t.Fatalf("expected 1 stored fact, got %d", len(repo.stored))
	}
}
```

Also update the two existing tests that call `memory.NewService(repo, &mockEmbedder{})` — they now need the config arg. Replace both calls with:

```go
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45, DedupThreshold: 0.15, TopK: 5, FactLimit: 10, SummaryDays: 7,
	})
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/app/memory/ -run TestMemoryService -v`
Expected: compile error / FAIL — `memory.NewService` does not take a `ServiceConfig` and `ServiceConfig` is undefined.

- [ ] **Step 4: Implement `ServiceConfig` and update the Service**

In `internal/app/memory/service.go`, replace the `Service` struct and `NewService` with:

```go
type ServiceConfig struct {
	SimilarityThreshold float64
	DedupThreshold      float64
	TopK                int
	FactLimit           int
	SummaryDays         int
}

type Service struct {
	repo     output.MemoryRepository
	embedder output.EmbeddingProvider
	cfg      ServiceConfig
}

func NewService(repo output.MemoryRepository, embedder output.EmbeddingProvider, cfg ServiceConfig) *Service {
	if cfg.TopK == 0 {
		cfg.TopK = 5
	}
	if cfg.FactLimit == 0 {
		cfg.FactLimit = 10
	}
	if cfg.SummaryDays == 0 {
		cfg.SummaryDays = 7
	}
	return &Service{repo: repo, embedder: embedder, cfg: cfg}
}
```

- [ ] **Step 5: Add dedup to `StoreFact`**

In `internal/app/memory/service.go`, replace the body of `StoreFact` with:

```go
func (s *Service) StoreFact(ctx context.Context, content, source string, tags []string) error {
	mem := entity.NewMemory(entity.MemoryFact, content, source, tags)

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		slog.Warn("failed to generate embedding, storing without", "error", err)
	} else {
		mem.Embedding = embedding
		if s.cfg.DedupThreshold > 0 {
			similar, serr := s.repo.SearchSimilarScored(ctx, embedding, entity.MemoryFact, 1)
			if serr != nil {
				slog.Warn("dedup search failed", "error", serr)
			} else if len(similar) > 0 && similar[0].Distance < s.cfg.DedupThreshold {
				slog.Info("skipping duplicate fact", "content", content, "distance", similar[0].Distance)
				return nil
			}
		}
	}

	return s.repo.Store(ctx, mem)
}
```

- [ ] **Step 6: Apply threshold + top-k in `BuildContext`**

In `internal/app/memory/service.go`, in `BuildContext`, replace the block that begins with `similar, err := s.repo.SearchSimilar(ctx, embedding, 5)` down to (but not including) the `facts, err := ...` line with:

```go
	scored, err := s.repo.SearchSimilarScored(ctx, embedding, "", s.cfg.TopK)
	if err != nil {
		return "", fmt.Errorf("search similar: %w", err)
	}
	var similar []*entity.Memory
	for _, sm := range scored {
		if s.cfg.SimilarityThreshold <= 0 || sm.Distance < s.cfg.SimilarityThreshold {
			similar = append(similar, sm.Memory)
		}
	}
```

Then update the two existing calls in `BuildContext` to use config values:
- change `s.repo.GetByType(ctx, entity.MemoryFact, 10)` to `s.repo.GetByType(ctx, entity.MemoryFact, s.cfg.FactLimit)`
- change `s.repo.GetRecentSummaries(ctx, 7)` to `s.repo.GetRecentSummaries(ctx, s.cfg.SummaryDays)`

(The rest of `BuildContext` — the `strings.Builder` section using `similar` — stays unchanged.)

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/app/memory/ -run TestMemoryService -v`
Expected: PASS for all four `TestMemoryService_*` tests.

- [ ] **Step 8: Commit**

```bash
git add internal/app/memory/service.go internal/app/memory/service_test.go
git commit -m "feat(memory): dedup facts and apply relevance threshold to RAG"
```

---

## Task 4: Real-time fact extractor

**Files:**
- Create: `internal/app/memory/extractor.go`
- Test: `internal/app/memory/extractor_test.go`

- [ ] **Step 1: Write the failing test for `parseFacts`**

Create `internal/app/memory/extractor_test.go`:

```go
package memory

import "testing"

func TestParseFacts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"none", "NONE", 0},
		{"none lowercase", "none", 0},
		{"empty", "", 0},
		{"bullets", "- user likes Go\n- user lives in Phnom Penh", 2},
		{"mixed prefix", "FACTS:\n* fact a\n- fact b\n  \n- fact c", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseFacts(c.in)
			if len(got) != c.want {
				t.Fatalf("parseFacts(%q) = %d facts, want %d (%v)", c.in, len(got), c.want, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/memory/ -run TestParseFacts -v`
Expected: FAIL — `undefined: parseFacts`.

- [ ] **Step 3: Implement the extractor**

Create `internal/app/memory/extractor.go`:

```go
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// Extractor pulls durable user facts from recent messages in the background.
type Extractor struct {
	svc      *Service
	msgRepo  output.MessageRepository
	llm      output.LLMProvider
	model    string
	interval int
}

func NewExtractor(svc *Service, msgRepo output.MessageRepository, llm output.LLMProvider, model string, interval int) *Extractor {
	if interval <= 0 {
		interval = 6
	}
	return &Extractor{svc: svc, msgRepo: msgRepo, llm: llm, model: model, interval: interval}
}

// Interval is the number of messages between extraction runs.
func (e *Extractor) Interval() int { return e.interval }

func (e *Extractor) Extract(ctx context.Context, convID uuid.UUID) error {
	messages, err := e.msgRepo.ListMessages(ctx, convID, e.interval)
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	var transcript []string
	for _, msg := range messages {
		if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
			transcript = append(transcript, fmt.Sprintf("[%s] %s", msg.Role, msg.Content))
		}
	}
	if len(transcript) == 0 {
		return nil
	}

	prompt := fmt.Sprintf(`From the conversation below, extract durable facts about the user (preferences, identity, projects, decisions). Ignore one-off chatter. If there is nothing worth remembering, reply with the single word NONE.

Conversation:
%s

Reply with a bullet list of facts, or NONE.`, strings.Join(transcript, "\n"))

	resp, err := e.llm.Chat(ctx, output.LLMRequest{
		Messages:    []output.LLMMessage{{Role: entity.RoleUser, Content: prompt}},
		MaxTokens:   300,
		Temperature: 0.2,
		Model:       e.model,
	})
	if err != nil {
		return fmt.Errorf("llm extract: %w", err)
	}

	facts := parseFacts(resp.Content)
	for _, f := range facts {
		if err := e.svc.StoreFact(ctx, f, "realtime", []string{"extracted", "realtime"}); err != nil {
			slog.Warn("failed to store extracted fact", "error", err)
		}
	}
	slog.Info("realtime extraction complete", "conv_id", convID, "facts", len(facts))
	return nil
}

// parseFacts turns a bullet list into trimmed fact strings. "NONE" yields nothing.
func parseFacts(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || strings.EqualFold(text, "NONE") {
		return nil
	}
	var facts []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(strings.ToUpper(line), "FACTS:") {
			continue
		}
		line = strings.TrimLeft(line, "-*•")
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "NONE") {
			continue
		}
		facts = append(facts, line)
	}
	return facts
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/memory/ -run TestParseFacts -v`
Expected: PASS for all sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/app/memory/extractor.go internal/app/memory/extractor_test.go
git commit -m "feat(memory): add real-time fact extractor"
```

---

## Task 5: Summarizer backfill and catch-up

**Files:**
- Modify: `internal/app/memory/summarizer.go`
- Test: `internal/app/memory/summarizer_test.go`

Replace the fragile "only at 23:55" trigger with a catch-up loop that summarizes any past day that has messages but no summary.

- [ ] **Step 1: Write the failing test for `missingDays`**

Create `internal/app/memory/summarizer_test.go`:

```go
package memory

import (
	"testing"
	"time"
)

func TestMissingDays(t *testing.T) {
	loc := time.UTC
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, loc) }

	withMessages := map[string]bool{
		"2026-05-21": true,
		"2026-05-22": true,
		"2026-05-23": true,
	}
	withSummary := map[string]bool{
		"2026-05-21": true,
	}
	today := d(2026, 5, 23) // 23rd is "today", must be excluded

	got := missingDays(withMessages, withSummary, today)

	if len(got) != 1 || got[0] != "2026-05-22" {
		t.Fatalf("expected [2026-05-22], got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/memory/ -run TestMissingDays -v`
Expected: FAIL — `undefined: missingDays`.

- [ ] **Step 3: Rewrite the summarizer**

Replace the entire contents of `internal/app/memory/summarizer.go` with:

```go
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const dayLayout = "2006-01-02"

type Summarizer struct {
	memorySvc     *Service
	messageRepo   output.MessageRepository
	llm           output.LLMProvider
	interval      time.Duration
	retentionDays int
}

func NewSummarizer(
	memorySvc *Service,
	messageRepo output.MessageRepository,
	llm output.LLMProvider,
	interval time.Duration,
	retentionDays int,
) *Summarizer {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	return &Summarizer{
		memorySvc:     memorySvc,
		messageRepo:   messageRepo,
		llm:           llm,
		interval:      interval,
		retentionDays: retentionDays,
	}
}

func (s *Summarizer) Run(ctx context.Context) {
	if err := s.CatchUp(ctx); err != nil {
		slog.Error("initial summarization catch-up failed", "error", err)
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CatchUp(ctx); err != nil {
				slog.Error("summarization catch-up failed", "error", err)
			}
		}
	}
}

// CatchUp summarizes every past day that has messages but no summary yet.
func (s *Summarizer) CatchUp(ctx context.Context) error {
	withMessages, err := s.daysWithMessages(ctx)
	if err != nil {
		return fmt.Errorf("days with messages: %w", err)
	}
	withSummary, err := s.daysWithSummary(ctx)
	if err != nil {
		return fmt.Errorf("days with summary: %w", err)
	}

	today := time.Now().Truncate(24 * time.Hour)
	for _, day := range missingDays(withMessages, withSummary, today) {
		parsed, perr := time.Parse(dayLayout, day)
		if perr != nil {
			continue
		}
		if serr := s.summarizeDay(ctx, parsed); serr != nil {
			slog.Error("failed to summarize day", "day", day, "error", serr)
		}
	}
	return nil
}

func (s *Summarizer) daysWithMessages(ctx context.Context) (map[string]bool, error) {
	convs, err := s.messageRepo.ListConversations(ctx, 100, 0)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	days := map[string]bool{}
	for _, conv := range convs {
		messages, merr := s.messageRepo.ListMessages(ctx, conv.ID, 1000)
		if merr != nil {
			slog.Warn("failed to list messages", "conv_id", conv.ID, "error", merr)
			continue
		}
		for _, msg := range messages {
			if msg.CreatedAt.Before(cutoff) {
				continue
			}
			if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
				days[msg.CreatedAt.Truncate(24*time.Hour).Format(dayLayout)] = true
			}
		}
	}
	return days, nil
}

func (s *Summarizer) daysWithSummary(ctx context.Context) (map[string]bool, error) {
	summaries, err := s.memorySvc.repo.GetRecentSummaries(ctx, s.retentionDays)
	if err != nil {
		return nil, err
	}
	days := map[string]bool{}
	for _, sum := range summaries {
		for _, tag := range sum.Tags {
			if strings.HasPrefix(tag, "day:") {
				days[strings.TrimPrefix(tag, "day:")] = true
			}
		}
	}
	return days, nil
}

// missingDays returns days (sorted) that have messages, no summary, and are before today.
func missingDays(withMessages, withSummary map[string]bool, today time.Time) []string {
	todayStr := today.Truncate(24 * time.Hour).Format(dayLayout)
	var out []string
	for day := range withMessages {
		if day == todayStr {
			continue
		}
		if day > todayStr {
			continue
		}
		if withSummary[day] {
			continue
		}
		out = append(out, day)
	}
	sort.Strings(out)
	return out
}

func (s *Summarizer) summarizeDay(ctx context.Context, day time.Time) error {
	dayStart := day.Truncate(24 * time.Hour)
	dayEnd := dayStart.AddDate(0, 0, 1)
	dayTag := dayStart.Format(dayLayout)
	slog.Info("summarizing day", "day", dayTag)

	convs, err := s.messageRepo.ListConversations(ctx, 100, 0)
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}

	var dayMessages []string
	for _, conv := range convs {
		messages, merr := s.messageRepo.ListMessages(ctx, conv.ID, 1000)
		if merr != nil {
			slog.Warn("failed to list messages", "conv_id", conv.ID, "error", merr)
			continue
		}
		for _, msg := range messages {
			if msg.CreatedAt.Before(dayStart) || !msg.CreatedAt.Before(dayEnd) {
				continue
			}
			if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
				dayMessages = append(dayMessages, fmt.Sprintf("[%s] %s", msg.Role, msg.Content))
			}
		}
	}
	if len(dayMessages) == 0 {
		slog.Info("no messages to summarize", "day", dayTag)
		return nil
	}

	prompt := fmt.Sprintf(`Summarize the day's conversations in 2-3 sentences. Then extract key facts about the user as a bullet list.

Conversations:
%s

Respond in this format:
SUMMARY: <2-3 sentence summary>
FACTS:
- <fact 1>
- <fact 2>`, strings.Join(dayMessages, "\n"))

	resp, err := s.llm.Chat(ctx, output.LLMRequest{
		Messages:    []output.LLMMessage{{Role: entity.RoleUser, Content: prompt}},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		return fmt.Errorf("llm summarize: %w", err)
	}

	parts := strings.SplitN(resp.Content, "FACTS:", 2)
	summaryText := strings.TrimSpace(strings.TrimPrefix(parts[0], "SUMMARY:"))
	if summaryText != "" {
		if err := s.memorySvc.StoreSummary(ctx, summaryText, "summarizer", []string{"daily", "day:" + dayTag}); err != nil {
			slog.Error("failed to store summary", "error", err)
		}
	}

	if len(parts) > 1 {
		for _, fact := range parseFacts(parts[1]) {
			if err := s.memorySvc.StoreFact(ctx, fact, "summarizer", []string{"extracted"}); err != nil {
				slog.Error("failed to store fact", "error", err)
			}
		}
	}

	slog.Info("day summarization complete", "day", dayTag, "messages_processed", len(dayMessages))
	return nil
}
```

Note: this reuses `parseFacts` from `extractor.go` (same package) — Task 4 must be done first.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/memory/ -run TestMissingDays -v`
Expected: PASS.

- [ ] **Step 5: Build**

Run: `go build ./internal/app/...`
Expected: FAIL — `cmd/assistant/main.go` still calls the old `NewSummarizer` (4 args) and old `NewService` (2 args). Fixed in Task 6. The memory package itself must build:

Run: `go vet ./internal/app/memory/`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/app/memory/summarizer.go internal/app/memory/summarizer_test.go
git commit -m "feat(memory): summarizer backfill and catch-up loop"
```

---

## Task 6: Wire real-time extraction into chat flow

**Files:**
- Modify: `internal/app/chat/service.go`
- Modify: `cmd/assistant/main.go`

- [ ] **Step 1: Add counter + extractor to `chat.Service`**

In `internal/app/chat/service.go`, update the imports to include `sync`, `time`, and `uuid`:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)
```

Replace the `Service` struct and `NewService` with:

```go
type Service struct {
	pipeline     *Pipeline
	messageRepo  output.MessageRepository
	activityRepo output.ActivityRepository
	memorySvc    *memory.Service
	extractor    *memory.Extractor
	systemPrompt string

	mu        sync.Mutex
	msgCounts map[uuid.UUID]int
}

func NewService(
	pipeline *Pipeline,
	messageRepo output.MessageRepository,
	activityRepo output.ActivityRepository,
	memorySvc *memory.Service,
	extractor *memory.Extractor,
	systemPrompt string,
) *Service {
	return &Service{
		systemPrompt: systemPrompt,
		pipeline:     pipeline,
		messageRepo:  messageRepo,
		activityRepo: activityRepo,
		memorySvc:    memorySvc,
		extractor:    extractor,
		msgCounts:    make(map[uuid.UUID]int),
	}
}
```

- [ ] **Step 2: Trigger extraction after the assistant reply is saved**

In `internal/app/chat/service.go`, in `ProcessMessage`, immediately after the `if err := s.messageRepo.SaveMessage(ctx, assistantMsg); err != nil { ... }` block and before the `if s.activityRepo != nil {` block, insert:

```go
	if s.extractor != nil {
		s.mu.Lock()
		s.msgCounts[conv.ID] += 2 // user + assistant
		trigger := s.msgCounts[conv.ID] >= s.extractor.Interval()
		if trigger {
			s.msgCounts[conv.ID] = 0
		}
		s.mu.Unlock()

		if trigger {
			go func(id uuid.UUID) {
				bg, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := s.extractor.Extract(bg, id); err != nil {
					slog.Warn("realtime fact extraction failed", "error", err)
				}
			}(conv.ID)
		}
	}
```

- [ ] **Step 3: Update wiring in `main.go`**

In `cmd/assistant/main.go`:

Replace the `memorySvc := memory.NewService(memoryRepo, embeddingClient)` line (~line 114) with:

```go
	memorySvc := memory.NewService(memoryRepo, embeddingClient, memory.ServiceConfig{
		SimilarityThreshold: cfg.Memory.SimilarityThreshold,
		DedupThreshold:      cfg.Memory.DedupThreshold,
		TopK:                cfg.Memory.WorkingMemoryResults,
		FactLimit:           10,
		SummaryDays:         7,
	})
	factExtractor := memory.NewExtractor(memorySvc, messageRepo, llmClient, cfg.Memory.ExtractionModel, cfg.Memory.FactExtractionInterval)
```

Replace the `chatService := chat.NewService(pipeline, messageRepo, activityRepo, memorySvc, systemPrompt)` line (~line 132) with:

```go
	chatService := chat.NewService(pipeline, messageRepo, activityRepo, memorySvc, factExtractor, systemPrompt)
```

Replace the `summarizer := memory.NewSummarizer(memorySvc, messageRepo, llmClient, cfg.Memory.SummarizeInterval)` line (~line 208) with:

```go
	summarizer := memory.NewSummarizer(memorySvc, messageRepo, llmClient, cfg.Memory.SummarizeInterval, cfg.Memory.RetentionDays)
```

- [ ] **Step 4: Build the whole project**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS (no failures). Pre-existing tests that need network (openrouter) may be skipped/short — if any pre-existing test was already failing before this work, note it but do not treat it as a regression.

- [ ] **Step 6: Commit**

```bash
git add internal/app/chat/service.go cmd/assistant/main.go
git commit -m "feat(chat): trigger real-time fact extraction every N messages"
```

---

## Task 7: Deploy and verify backfill

**Files:** none (operational).

- [ ] **Step 1: Confirm the build and tests are green**

Run: `go build ./... && go test ./...`
Expected: success.

- [ ] **Step 2: Deploy**

Run: `make deploy`
Expected: builds linux binary, uploads, restarts both services. (This restarts the live bots — confirm with the user before running.)

- [ ] **Step 3: Verify backfill ran**

Run:
```bash
ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 "journalctl -u assistant --since '5 min ago' | grep -i 'summariz\|extraction'"
```
Expected: log lines like `summarizing day day=2026-05-22` for the previously missing days.

- [ ] **Step 4: Verify new memory rows**

Run:
```bash
ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 "sudo -u postgres psql -d assistant -t -c \"SELECT type, count(*), max(created_at) FROM memories GROUP BY type;\""
```
Expected: summary count increased to cover the backfilled days (was 1); `max(created_at)` is today.

- [ ] **Step 5: Verify real-time extraction**

Send the Oleg bot ~3 short exchanges in Telegram, then re-run the query from Step 4.
Expected: at least one new `fact` row with `source = 'realtime'`:
```bash
ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 "sudo -u postgres psql -d assistant -t -c \"SELECT content FROM memories WHERE source='realtime' ORDER BY created_at DESC LIMIT 5;\""
```

---

## Self-Review Notes

- **Spec coverage:** (1) reliable daily + backfill → Task 5; (2) real-time facts every N → Tasks 4 + 6; (3) relevance threshold → Tasks 2 + 3 (Step 6); (4) dedup → Tasks 2 + 3 (Step 5); config → Task 1. All covered.
- **Type consistency:** `SearchSimilarScored` / `ScoredMemory` defined in Task 2 and used identically in Tasks 3, 5. `ServiceConfig` defined in Task 3, used in Task 6. `parseFacts` defined in Task 4, reused in Task 5. `NewService`/`NewSummarizer`/`NewExtractor` signatures match between definition and call sites.
- **Known minor behavior:** the pre-existing untagged May 21 summary lacks a `day:` tag, so May 21 will be re-summarized once (creating one tagged duplicate). Acceptable and self-correcting.
- **Build ordering:** Task 2 Step 4 and Task 5 Step 5 intentionally leave `go build ./...` red until Task 6 updates call sites; scoped build commands are given to verify partial progress.
