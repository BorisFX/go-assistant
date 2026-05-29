# Memory / RAG Fixes — Design

Date: 2026-05-29
Status: approved

## Problem

The bot's long-term memory is effectively frozen. Diagnosis against the live
`assistant` database (Oleg / @debil4bot):

- Only 7 memory rows total (6 facts + 1 summary), all dated `2026-05-21`, while
  233 messages have accumulated since.
- Daily summarizer fires only at 23:55 once per day (`summarizer.go:45`) and
  requires the process to be alive at that exact minute. A single restart in that
  window loses the day permanently — which is why nothing has been written since
  May 21.
- Within a day the bot stores no new facts at all; facts only come from the
  nightly summarizer.
- `SearchSimilar` has no relevance threshold (`memory_repo.go:73`): it always
  returns the top-5 nearest rows regardless of distance, so irrelevant memories
  get injected into every request.

Net effect: RAG runs on 8-day-old, noisy memory and is functionally useless.

## Goals

1. Daily summarization that never silently loses days (backfill + catch-up).
2. Real-time fact extraction, batched every N messages, in the background.
3. Relevance threshold on retrieval.
4. Deduplication of facts before storing.

Non-goals: replacing the embedding model, reworking the vector store, changing
the dashboard. Vision-model work is tracked separately.

## Design

### 1. Reliable daily summarization (backfill + catch-up)

- Refactor `Summarizer.Summarize(ctx)` into `summarizeDay(ctx, day time.Time)`
  so any specific past day can be summarized, not just "today".
- Add a way to detect which days need summarizing: for each completed day
  (strictly before today) that has user/assistant messages but no `summary`
  memory tagged with that date, the day is "missing".
  - Summary memories get a date tag (e.g. `day:2026-05-21`) so missing-day
    detection is a set difference between days-with-messages and days-with-summary.
- `Summarizer.Run`: on startup, and then on each tick (hourly), compute missing
  days and summarize them one by one. This auto-backfills May 22–28 on first
  deploy and self-heals after any future downtime.
- Keep the existing end-of-day behavior as a natural consequence (yesterday
  becomes "missing" once the clock rolls past midnight).

### 2. Real-time fact extraction (batched, background)

- Maintain a per-conversation processed-message counter. After each assistant
  reply is persisted, when unprocessed messages reach
  `memory.fact_extraction_interval` (default 6), trigger extraction.
- Extraction runs in a background goroutine with its own context + timeout so it
  never blocks the Telegram reply:
  - take the last N user/assistant messages,
  - call `memory.extraction_model` (default `deepseek/deepseek-v4-flash`) with a
    prompt: "extract durable facts about the user from this conversation; reply
    with a bullet list or the single word NONE",
  - for each fact, run the dedup check (below) then `StoreFact`.

### 3. Relevance threshold on retrieval

- `SearchSimilar` gains a max-distance filter:
  `WHERE embedding <=> $1 < $threshold ORDER BY embedding <=> $1 LIMIT $k`.
- Threshold from `memory.similarity_threshold` (default 0.45 cosine distance,
  tunable). `BuildContext` uses the configured top-k (`WorkingMemoryResults`)
  instead of the hardcoded 5.

### 4. Fact deduplication

- Before `StoreFact` (both real-time and summarizer paths), embed the candidate
  and find the nearest existing fact. If distance < `memory.dedup_threshold`
  (default 0.15), skip storing. Keeps near-duplicate facts out.

### Config additions (`MemoryConfig`)

```yaml
memory:
  # existing: short_term_limit, working_memory_results, max_context_tokens,
  #           retention_days, summarize_interval
  fact_extraction_interval: 6
  extraction_model: "deepseek/deepseek-v4-flash"
  similarity_threshold: 0.45
  dedup_threshold: 0.15
```

Defaults applied in code so existing configs keep working.

## Testing

- Unit: missing-day detection (days-with-messages minus days-with-summary).
- Unit: dedup skip when nearest-fact distance < threshold; store when above.
- Unit/repo: `SearchSimilar` excludes rows beyond the distance threshold.
- Manual: deploy, confirm May 22–28 backfilled; send N messages and confirm a
  new fact row appears with `source` from the real-time extractor.

## Affected code

- `internal/app/memory/summarizer.go` — day-targeted summarize + catch-up loop.
- `internal/app/memory/service.go` — dedup in StoreFact; threshold/top-k in
  BuildContext.
- `internal/adapter/driven/postgres/memory_repo.go` — threshold in SearchSimilar;
  missing-day query support.
- `internal/app/chat/service.go` (or pipeline) — per-conversation counter +
  trigger real-time extraction.
- New: real-time fact extractor (small unit in `internal/app/memory/`).
- `pkg/config/config.go` + `configs/config.example.yaml` — new MemoryConfig fields.
