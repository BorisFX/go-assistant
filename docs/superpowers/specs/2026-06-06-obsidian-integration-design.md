# Obsidian Integration for @debil4bot — Design

**Date:** 2026-06-06
**Status:** Approved design, pending plan
**Scope:** Wire the Obsidian vault (`~/Obsidian/Oleg`, git-synced) into the Go Assistant bot as a pluggable, instance-gated module, plus the vault-side slash commands it delegates to.

## Goal

Give the Oleg instance (`@debil4bot`) three Obsidian workflows — weekly review, research ingest, idea cross-pollination — plus the ability to use the vault as a personal-knowledge source. The capability must be a **separate pluggable module loaded only for Oleg's instance**; Yuri's instance must not see it.

## Non-goals (YAGNI)

- No pgvector indexing of the vault — start with `grep`/file reads.
- No separate MCP server — the bot's tool registry is the integration surface.
- No Mac-side cron for weekly review — the bot's Postgres cron on Linode owns scheduling.
- No meeting-processor workflow (deferred).
- No on-demand "run from Telegram" as a dedicated feature — it falls out of Phase 2 for free (the LLM can call the `obsidian` tool when asked).

## Architecture overview

Three layers, built in order:

1. **Vault slash commands** (markdown, in the vault git repo) — the single source of truth for the three workflows. Usable locally on Mac and on Linode; versioned with the vault.
2. **`obsidian` bot module** (Go, in `go-assistant`) — one pluggable tool that the LLM/cron can call. Native `search`/`read`; the three workflows delegate to Claude Code running the slash commands in the vault dir.
3. **Linode deploy + wiring** — vault cloned + git-synced on the bot host; config enables the module only for Oleg; weekly cron job seeded; `system-prompt.md` rules for ingest-by-forward and vault-as-memory.

---

## Phase 1 — Vault slash commands

Location: `~/Obsidian/Oleg/.claude/commands/`. New folders: `_agent/inbox/` (ingest input), `300_Области/Источники/` (ingest output — a real knowledge folder so backlinks work in the graph; deliberate exception to the "agent output → `_agent/`" rule).

All outputs in **Russian**, with the vault's frontmatter convention (`tags: agent, …`, `date`, `agent`).

### `weekly-review.md`
- Determine changed notes over the last 7 days via `git log --since="7 days ago" --name-only` plus `find . -name '*.md' -mtime -7` (covers uncommitted edits). Exclude `.git`, `_agent` scratch where appropriate.
- Read the changed notes; write `_agent/reports/YYYY-MM-DD-weekly-review.md`.
- Sections: что сделано · принятые решения · прогресс по проектам · замеченные паттерны · приоритеты на следующую неделю.
- Keep it short; this is a read-in-2-minutes artifact.

### `ingest.md`
- Read the newest file in `_agent/inbox/`.
- Create a summary note in `300_Области/Источники/`: metadata (title, author, date, URL if present), 3–5 takeaways, key insights.
- Search the vault for related notes; add two-way links. Flag any point in the new source that contradicts existing notes.
- Clear the processed inbox file.

### `cross-pollinate.md`
- Takes a note path as argument (`$ARGUMENTS`).
- Searches the whole vault; surfaces 5 non-obvious connections from unrelated areas, one sentence per hidden bridge.
- Prints to chat; offers to append a `## Связи` section to the idea note.

**Testing (Phase 1):** run each command locally on the Mac against the real vault; verify output files, frontmatter, links, and that `ingest` clears the inbox. No automated tests (markdown commands).

---

## Phase 2 — `obsidian` bot module (Go, TDD)

New file: `internal/tooling/builtin/obsidian.go` (+ `obsidian_test.go`). One tool, `obsidian`, with an `action` parameter. Implements `output.Tool` (`Name/Description/Category/Schema/Execute`).

### Actions
| action | impl | behaviour |
|--------|------|-----------|
| `search` | native Go | recursive `.md` search under `vault_dir` (filename + content match), returns ranked path + snippet list |
| `read` | native Go | read a note by relative path, return content (size-capped) |
| `ingest` | delegate | write provided `content` to `_agent/inbox/<slug>.md`, then run `/ingest` via Claude Code in `vault_dir` |
| `weekly_review` | delegate | run `/weekly-review` via Claude Code in `vault_dir`; return the report path + summary |
| `cross_pollinate` | delegate | run `/cross-pollinate <note>` via Claude Code in `vault_dir` |

### Dependencies & construction
- `NewObsidian(vaultDir string, executor CodeExecutor) *Obsidian` — reuses the existing `CodeExecutor` interface (`ExecuteJSON(ctx, prompt, workDir, onProgress)`), same one `run_code` uses. Delegating actions call `ExecuteJSON(ctx, "/weekly-review", vaultDir, nil)` etc.
- Native actions (`search`/`read`) do plain filesystem ops rooted at `vaultDir`. Path traversal guard: resolve and confirm the target stays within `vaultDir`.

### Schema
Single object schema with `action` (enum, required) and optional `query`, `path`, `content`, `note` fields. Description tells the LLM which fields each action needs.

### Config & gating
- `pkg/config/config.go`: add `Obsidian Obsidian` field (`yaml:"obsidian"`) with `VaultDir string `yaml:"vault_dir"``.
- `cmd/assistant/main.go`: `if cfg.Obsidian.VaultDir != "" { registry.Register(builtin.NewObsidian(cfg.Obsidian.VaultDir, codeExecutor)) }` — mirrors the `Trading`/`MailRu` gating pattern. Only Oleg's `config.yaml` sets `obsidian.vault_dir`.
- `configs/config.example.yaml`: document the `obsidian:` section (commented/empty).

### Testing (Phase 2, TDD — every builtin has a `_test.go`)
- Schema is valid JSON and lists the action enum.
- `Execute` dispatches per `action`; unknown action → error.
- `search`/`read` against a temp vault dir (table tests): match, no-match, path-traversal rejection, size cap.
- Delegating actions: inject a fake `CodeExecutor`, assert it's called with the right slash command and `workDir == vaultDir`; `ingest` first writes the inbox file.
- Gating: with empty `VaultDir` the tool is not registered (assert via registry list in a wiring test or documented manual check).

---

## Phase 3 — Linode deploy + wiring (prod, step-by-step with confirmation)

Bot runs on the Linode host (config `searxng_url: localhost:8888`). Claude Code is already installed/authed there (the existing `run_code` tool proves it) — confirm before relying on it.

1. **Vault on Linode:** clone `~/Obsidian/Oleg` to a path on the bot host; add a git-sync cron (pull/push) mirroring `scripts/vault-sync.sh` so Phase-1 commands and notes stay current.
2. **Enable module for Oleg only:** set `obsidian.vault_dir` in Oleg's `config.yaml` (stored in the vault at `200_Проекты/Go-Assistant/configs-private/oleg/`). Leave Yuri's config untouched.
3. **Weekly cron job:** add a bot cron job (via Telegram `manage_cron` or seed), schedule `пт 17:00`, prompt instructs the bot to call `obsidian` `weekly_review` and relay the result. Cron scheduler runs the prompt through the chat pipeline and `send()`s `resp.Content` to Telegram (`internal/app/cron/scheduler.go`).
4. **`system-prompt.md` rules** (vault-stored, Oleg only):
   - *Ingest-by-forward:* when the user forwards an article/PDF/voice and asks to save it, extract the text (existing vision/voice/PDF tooling), call `obsidian` `ingest` with that content.
   - *Vault-as-memory:* for personal facts/notes, call `obsidian` `search`/`read` before answering.

### Testing (Phase 3)
- After deploy: `obsidian search`/`read` from Telegram returns real notes.
- Forward a test article → confirm a note appears in `300_Области/Источники/` and inbox is cleared.
- Trigger the cron job manually → confirm the weekly report posts to Telegram and the file lands in the vault.
- Confirm Yuri's instance has no `obsidian` tool.

---

## Risks / notes

- Phases 2–3 touch the production bot and a live `system-prompt.md` — apply step by step with confirmation; keep the Go change behind the config gate so a missing `vault_dir` is a no-op.
- Claude Code auth on Linode is a hard prerequisite for the delegating actions — confirm before Phase 3.
- `claude -p "/weekly-review"` must resolve the vault's `.claude/commands/` — ensure `work_dir` is the vault root, not a subdir.
- Long-running delegating actions: the bot already streams `run_code`; reuse the same executor so timeouts/`--max-turns` behaviour is consistent.

## Build order

Phase 1 (standalone, locally testable) → Phase 2 (Go module + tests, locally testable) → Phase 3 (prod deploy, gated, step-by-step).
