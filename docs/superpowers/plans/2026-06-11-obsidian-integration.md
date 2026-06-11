# Obsidian Integration for @debil4bot — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pluggable, instance-gated `obsidian` module to the Go Assistant bot that searches/reads the vault and runs three workflows (weekly review, research ingest, idea cross-pollination), backed by vault-side slash commands.

**Architecture:** Vault slash commands are the single source of truth for the three workflows (markdown in the vault git repo). The bot exposes one `obsidian` tool: `search`/`read` are native Go file ops over `vault_dir`; `ingest`/`weekly_review`/`cross_pollinate` delegate to Claude Code (via the existing `CodeExecutor`) running the slash commands in `vault_dir`. The tool registers only when `obsidian.vault_dir` is set in config — Oleg's instance only.

**Tech Stack:** Go 1.22 (hexagonal monolith, `internal/tooling/builtin`), Claude Code slash commands (markdown), YAML config, Postgres-backed bot cron.

**Repos & paths:**
- Bot: `~/Programming/go-assistant` (Phase 2 Go code, this plan lives here).
- Vault: `~/Obsidian/Oleg` (Phase 1 markdown commands, Phase 3 config/system-prompt).

---

## File Structure

**Phase 1 (vault repo `~/Obsidian/Oleg`):**
- Create: `.claude/commands/weekly-review.md`
- Create: `.claude/commands/ingest.md`
- Create: `.claude/commands/cross-pollinate.md`
- Create: `_agent/inbox/.gitkeep`
- Create: `300_Области/Источники/.gitkeep`

**Phase 2 (bot repo `~/Programming/go-assistant`):**
- Create: `internal/tooling/builtin/obsidian.go` — the `obsidian` tool (one responsibility: vault access + workflow delegation).
- Create: `internal/tooling/builtin/obsidian_test.go` — unit tests.
- Modify: `pkg/config/config.go` — add `Obsidian` struct + `Config.Obsidian` field.
- Modify: `cmd/assistant/main.go` — gated registration.
- Modify: `configs/config.example.yaml` — document the `obsidian:` section.

**Phase 3 (Linode + vault config):** ops checklist, no new source files.

---

## Phase 1 — Vault slash commands

### Task 1: Create the vault folders

**Files:**
- Create: `~/Obsidian/Oleg/_agent/inbox/.gitkeep`
- Create: `~/Obsidian/Oleg/300_Области/Источники/.gitkeep`

- [ ] **Step 1: Create the folders with keep files**

```bash
cd ~/Obsidian/Oleg
mkdir -p _agent/inbox "300_Области/Источники"
touch _agent/inbox/.gitkeep "300_Области/Источники/.gitkeep"
```

- [ ] **Step 2: Verify**

Run: `ls -la ~/Obsidian/Oleg/_agent/inbox "~/Obsidian/Oleg/300_Области/Источники"`
Expected: both directories exist, each containing `.gitkeep`.

- [ ] **Step 3: Commit (in vault repo)**

```bash
cd ~/Obsidian/Oleg
git add _agent/inbox/.gitkeep "300_Области/Источники/.gitkeep"
git commit -m "agent: add inbox and Источники folders for obsidian workflows"
```

---

### Task 2: Write `/weekly-review` command

**Files:**
- Create: `~/Obsidian/Oleg/.claude/commands/weekly-review.md`

- [ ] **Step 1: Write the command file**

Create `~/Obsidian/Oleg/.claude/commands/weekly-review.md`:

```markdown
---
description: Сводка изменений в волте за последние 7 дней
---

Собери еженедельный обзор моего Obsidian-волта. Пиши по-русски.

Шаги:
1. Найди ноты, менявшиеся за последние 7 дней:
   - `git log --since="7 days ago" --name-only --pretty=format: -- '*.md' | sort -u`
   - плюс `find . -name '*.md' -mtime -7 -not -path './.git/*'` (ловит несохранённые правки)
   Объедини списки, убери дубли, исключи `.git/` и служебные файлы из `_agent/` (кроме `_agent/reports`).
2. Прочитай эти ноты.
3. Создай файл `_agent/reports/<СЕГОДНЯ в формате YYYY-MM-DD>-weekly-review.md` с фронтматтером:
   ```
   ---
   tags: agent, weekly-review
   date: <YYYY-MM-DD>
   agent: claude-code
   ---
   ```
   и секциями:
   - **Что сделано** — конкретные результаты за неделю.
   - **Принятые решения** — что решено и почему.
   - **Прогресс по проектам** — по активным проектам из `200_Проекты`.
   - **Замеченные паттерны** — что повторяется, на что обратить внимание.
   - **Приоритеты на следующую неделю** — 3–5 пунктов.
4. Держи кратко — это читается за 2 минуты. В конце выведи путь к созданному файлу.
```

- [ ] **Step 2: Test the command locally**

Run: `cd ~/Obsidian/Oleg && claude -p "/weekly-review"`
Expected: a new file `_agent/reports/YYYY-MM-DD-weekly-review.md` is created with the frontmatter and five Russian sections; the command prints its path.

- [ ] **Step 3: Commit**

```bash
cd ~/Obsidian/Oleg
git add .claude/commands/weekly-review.md
git commit -m "agent: add /weekly-review slash command"
```

---

### Task 3: Write `/ingest` command

**Files:**
- Create: `~/Obsidian/Oleg/.claude/commands/ingest.md`

- [ ] **Step 1: Write the command file**

Create `~/Obsidian/Oleg/.claude/commands/ingest.md`:

```markdown
---
description: Превратить материал из _agent/inbox в summary-ноту и связать с волтом
---

Обработай самый свежий материал из `_agent/inbox/`. Пиши по-русски.

Шаги:
1. Возьми самый недавно изменённый файл в `_agent/inbox/` (исключая `.gitkeep`). Если папка пуста — сообщи и остановись.
2. Прочитай его. Извлеки: заголовок, автора, дату, URL (если есть) — это метаданные источника.
3. Создай summary-ноту в `300_Области/Источники/<краткий-слаг>.md` с фронтматтером:
   ```
   ---
   tags: agent, источник
   date: <YYYY-MM-DD>
   agent: claude-code
   ---
   ```
   и содержимым:
   - **Источник** — заголовок, автор, дата, URL.
   - **Ключевые идеи** — суть материала.
   - **3–5 тейкауэев** — списком.
4. Найди в волте ноты по той же теме (`grep`/поиск по содержимому). Проставь двусторонние ссылки `[[...]]`: из новой ноты на похожие и обратно.
5. Если что-то в источнике противоречит тому, что я уже писал в волте — добавь секцию **⚠️ Противоречия** с конкретными ссылками.
6. Удали обработанный файл из `_agent/inbox/`.
7. Выведи путь к созданной ноте и список связанных нот.
```

- [ ] **Step 2: Test the command locally**

```bash
cd ~/Obsidian/Oleg
printf '# Test article\nBy Jane Doe, 2026-06-11\nGo is great for backends.\n' > _agent/inbox/test-article.md
claude -p "/ingest"
```
Expected: a new note in `300_Области/Источники/`, two-way links added where relevant, and `_agent/inbox/test-article.md` removed.

- [ ] **Step 3: Commit**

```bash
cd ~/Obsidian/Oleg
git add .claude/commands/ingest.md
git commit -m "agent: add /ingest slash command"
```

---

### Task 4: Write `/cross-pollinate` command

**Files:**
- Create: `~/Obsidian/Oleg/.claude/commands/cross-pollinate.md`

- [ ] **Step 1: Write the command file**

Create `~/Obsidian/Oleg/.claude/commands/cross-pollinate.md`:

```markdown
---
description: Найти 5 неочевидных связей ноты с остальным волтом
argument-hint: <путь к ноте>
---

Тебе дана нота: `$ARGUMENTS`. Пиши по-русски.

Шаги:
1. Прочитай ноту по указанному пути. Если путь не дан или файл не найден — попроси указать ноту и остановись.
2. Прочитай/просканируй остальной волт (`grep`, чтение `.md`).
3. Найди ровно 5 неочевидных связей с нотами из, на первый взгляд, несвязанных областей. Для каждой:
   - ссылка `[[имя-ноты]]`
   - одно предложение про скрытый мостик (почему это связано неочевидно).
4. Выведи список в чат.
5. Спроси, дописать ли в исходную ноту секцию `## Связи` с этими пятью ссылками. Дописывай только после моего подтверждения.
```

- [ ] **Step 2: Test the command locally**

Run: `cd ~/Obsidian/Oleg && claude -p "/cross-pollinate 200_Проекты/Go-Assistant/README.md"`
Expected: 5 связей printed, each with a `[[note]]` link and a one-sentence hidden bridge; prompts before appending.

- [ ] **Step 3: Commit**

```bash
cd ~/Obsidian/Oleg
git add .claude/commands/cross-pollinate.md
git commit -m "agent: add /cross-pollinate slash command"
```

---

## Phase 2 — `obsidian` bot module (Go, TDD)

> All commands run from `~/Programming/go-assistant`. The repo uses standard `go test ./...`.

### Task 5: Add the `Obsidian` config struct

**Files:**
- Modify: `pkg/config/config.go`

- [ ] **Step 1: Add the struct and field**

In `pkg/config/config.go`, add a field to the `Config` struct (next to `Code` / `MailRu`):

```go
	Obsidian  Obsidian  `yaml:"obsidian"`
```

And add the struct definition (near the other section structs like `Code`):

```go
type Obsidian struct {
	VaultDir string `yaml:"vault_dir"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/config/config.go
git commit -m "feat(config): add obsidian.vault_dir section"
```

---

### Task 6: `obsidian` tool — schema, construction, dispatch (failing test first)

**Files:**
- Create: `internal/tooling/builtin/obsidian.go`
- Test: `internal/tooling/builtin/obsidian_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tooling/builtin/obsidian_test.go`:

```go
package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeExecutor struct {
	gotPrompt  string
	gotWorkDir string
	result     json.RawMessage
}

func (f *fakeExecutor) ExecuteJSON(ctx context.Context, prompt, workDir string, onProgress func(string)) (json.RawMessage, error) {
	f.gotPrompt = prompt
	f.gotWorkDir = workDir
	if f.result == nil {
		return json.RawMessage(`{"output":"ok"}`), nil
	}
	return f.result, nil
}

func TestObsidianMetadata(t *testing.T) {
	o := builtin.NewObsidian("/tmp/vault", &fakeExecutor{})
	if o.Name() != "obsidian" {
		t.Errorf("Name = %q, want obsidian", o.Name())
	}
	if o.Category() == "" {
		t.Error("Category is empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(o.Schema(), &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
}

func TestObsidianUnknownAction(t *testing.T) {
	o := builtin.NewObsidian("/tmp/vault", &fakeExecutor{})
	_, err := o.Execute(context.Background(), json.RawMessage(`{"action":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "action") {
		t.Errorf("expected unknown-action error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: FAIL — `undefined: builtin.NewObsidian`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tooling/builtin/obsidian.go`:

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
)

type Obsidian struct {
	vaultDir string
	executor CodeExecutor
}

func NewObsidian(vaultDir string, executor CodeExecutor) *Obsidian {
	return &Obsidian{vaultDir: vaultDir, executor: executor}
}

func (o *Obsidian) Name() string     { return "obsidian" }
func (o *Obsidian) Category() string { return "knowledge" }
func (o *Obsidian) Description() string {
	return "Access the personal Obsidian vault: search and read notes, or run workflows (ingest research, weekly_review, cross_pollinate)"
}

func (o *Obsidian) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["search", "read", "ingest", "weekly_review", "cross_pollinate"],
				"description": "search: find notes by text; read: read a note by relative path; ingest: save 'content' to the inbox and summarize it into the vault; weekly_review: generate the weekly report; cross_pollinate: find non-obvious links for the note in 'note'"
			},
			"query": {"type": "string", "description": "search: text to look for in filenames and note content"},
			"path": {"type": "string", "description": "read: note path relative to the vault root"},
			"content": {"type": "string", "description": "ingest: raw text (article/transcript) to file into the vault"},
			"note": {"type": "string", "description": "cross_pollinate: vault-relative path of the idea note"}
		},
		"required": ["action"]
	}`)
}

type obsidianParams struct {
	Action  string `json:"action"`
	Query   string `json:"query"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Note    string `json:"note"`
}

func (o *Obsidian) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p obsidianParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	switch p.Action {
	case "search":
		return o.search(p.Query)
	case "read":
		return o.read(p.Path)
	case "ingest":
		return o.ingest(ctx, p.Content)
	case "weekly_review":
		return o.delegate(ctx, "/weekly-review")
	case "cross_pollinate":
		return o.delegate(ctx, "/cross-pollinate "+p.Note)
	default:
		return nil, fmt.Errorf("unknown action %q", p.Action)
	}
}

func (o *Obsidian) delegate(ctx context.Context, command string) (json.RawMessage, error) {
	return o.executor.ExecuteJSON(ctx, command, o.vaultDir, nil)
}
```

This will not compile yet — `search`, `read`, `ingest` are added in Tasks 7–8. To get Tasks 6 tests green first, add temporary stubs at the end of the file:

```go
func (o *Obsidian) search(query string) (json.RawMessage, error) { return nil, fmt.Errorf("not implemented") }
func (o *Obsidian) read(path string) (json.RawMessage, error)    { return nil, fmt.Errorf("not implemented") }
func (o *Obsidian) ingest(ctx context.Context, content string) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented")
}
```

(Tasks 7 and 8 replace these stubs with real implementations.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: PASS for `TestObsidianMetadata` and `TestObsidianUnknownAction`.

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/obsidian.go internal/tooling/builtin/obsidian_test.go
git commit -m "feat(obsidian): add tool scaffold with schema and action dispatch"
```

---

### Task 7: Implement `search` and `read` (native, with path guard)

**Files:**
- Modify: `internal/tooling/builtin/obsidian.go`
- Test: `internal/tooling/builtin/obsidian_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tooling/builtin/obsidian_test.go`:

```go
import (
	"os"            // add to the existing import block
	"path/filepath" // add to the existing import block
)

func writeVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("200/go.md", "# Go Assistant\nbuilt in golang\n")
	must("300/health.md", "# Health\nsleep and water\n")
	return dir
}

func TestObsidianSearch(t *testing.T) {
	o := builtin.NewObsidian(writeVault(t), &fakeExecutor{})
	out, err := o.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"golang"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "go.md") {
		t.Errorf("search did not find go.md: %s", out)
	}
	if strings.Contains(string(out), "health.md") {
		t.Errorf("search matched unrelated note: %s", out)
	}
}

func TestObsidianRead(t *testing.T) {
	o := builtin.NewObsidian(writeVault(t), &fakeExecutor{})
	out, err := o.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"300/health.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sleep and water") {
		t.Errorf("read returned wrong content: %s", out)
	}
}

func TestObsidianReadPathTraversal(t *testing.T) {
	o := builtin.NewObsidian(writeVault(t), &fakeExecutor{})
	_, err := o.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"../../etc/passwd"}`))
	if err == nil {
		t.Error("expected path-traversal rejection, got nil error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: FAIL — `search`/`read` return "not implemented".

- [ ] **Step 3: Replace the stubs with real implementations**

In `internal/tooling/builtin/obsidian.go`, update the import block to:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)
```

Replace the `search` and `read` stubs with:

```go
const maxReadBytes = 100_000

// resolveInVault joins rel onto vaultDir and rejects escapes.
func (o *Obsidian) resolveInVault(rel string) (string, error) {
	abs := filepath.Join(o.vaultDir, filepath.Clean("/"+rel))
	root, err := filepath.Abs(o.vaultDir)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the vault", rel)
	}
	return full, nil
}

type searchHit struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

func (o *Obsidian) search(query string) (json.RawMessage, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("search requires a non-empty query")
	}
	q := strings.ToLower(query)
	var hits []searchHit
	err := filepath.WalkDir(o.vaultDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(o.vaultDir, path)
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(body)
		nameMatch := strings.Contains(strings.ToLower(rel), q)
		idx := strings.Index(strings.ToLower(text), q)
		if !nameMatch && idx < 0 {
			return nil
		}
		snippet := ""
		if idx >= 0 {
			start := idx - 40
			if start < 0 {
				start = 0
			}
			end := idx + 80
			if end > len(text) {
				end = len(text)
			}
			snippet = strings.ReplaceAll(text[start:end], "\n", " ")
		}
		hits = append(hits, searchHit{Path: rel, Snippet: snippet})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search vault: %w", err)
	}
	return json.Marshal(map[string]any{"query": query, "hits": hits})
}

func (o *Obsidian) read(rel string) (json.RawMessage, error) {
	if strings.TrimSpace(rel) == "" {
		return nil, fmt.Errorf("read requires a path")
	}
	full, err := o.resolveInVault(rel)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read note: %w", err)
	}
	truncated := false
	if len(body) > maxReadBytes {
		body = body[:maxReadBytes]
		truncated = true
	}
	return json.Marshal(map[string]any{"path": rel, "content": string(body), "truncated": truncated})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: PASS for search, read, and path-traversal tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/obsidian.go internal/tooling/builtin/obsidian_test.go
git commit -m "feat(obsidian): native search and read with path-traversal guard"
```

---

### Task 8: Implement `ingest` and verify delegation

**Files:**
- Modify: `internal/tooling/builtin/obsidian.go`
- Test: `internal/tooling/builtin/obsidian_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tooling/builtin/obsidian_test.go`:

```go
func TestObsidianIngestWritesInboxAndDelegates(t *testing.T) {
	vault := writeVault(t)
	if err := os.MkdirAll(filepath.Join(vault, "_agent", "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx := &fakeExecutor{}
	o := builtin.NewObsidian(vault, fx)

	_, err := o.Execute(context.Background(), json.RawMessage(`{"action":"ingest","content":"hello vault"}`))
	if err != nil {
		t.Fatal(err)
	}
	// content landed in inbox
	entries, _ := os.ReadDir(filepath.Join(vault, "_agent", "inbox"))
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			found = true
		}
	}
	if !found {
		t.Error("ingest did not write a file to _agent/inbox")
	}
	// delegated to /ingest in the vault dir
	if fx.gotPrompt != "/ingest" {
		t.Errorf("delegate prompt = %q, want /ingest", fx.gotPrompt)
	}
	if fx.gotWorkDir != vault {
		t.Errorf("delegate workDir = %q, want %q", fx.gotWorkDir, vault)
	}
}

func TestObsidianWeeklyReviewDelegates(t *testing.T) {
	vault := writeVault(t)
	fx := &fakeExecutor{}
	o := builtin.NewObsidian(vault, fx)
	if _, err := o.Execute(context.Background(), json.RawMessage(`{"action":"weekly_review"}`)); err != nil {
		t.Fatal(err)
	}
	if fx.gotPrompt != "/weekly-review" || fx.gotWorkDir != vault {
		t.Errorf("weekly_review delegated wrong: prompt=%q workDir=%q", fx.gotPrompt, fx.gotWorkDir)
	}
}

func TestObsidianCrossPollinateDelegates(t *testing.T) {
	vault := writeVault(t)
	fx := &fakeExecutor{}
	o := builtin.NewObsidian(vault, fx)
	if _, err := o.Execute(context.Background(), json.RawMessage(`{"action":"cross_pollinate","note":"200/go.md"}`)); err != nil {
		t.Fatal(err)
	}
	if fx.gotPrompt != "/cross-pollinate 200/go.md" {
		t.Errorf("cross_pollinate prompt = %q", fx.gotPrompt)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: FAIL — `ingest` returns "not implemented" (weekly/cross tests should already pass from Task 6's `delegate`).

- [ ] **Step 3: Replace the `ingest` stub with the real implementation**

In `internal/tooling/builtin/obsidian.go`, replace the `ingest` stub with:

```go
func (o *Obsidian) ingest(ctx context.Context, content string) (json.RawMessage, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("ingest requires content")
	}
	inbox := filepath.Join(o.vaultDir, "_agent", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return nil, fmt.Errorf("ensure inbox: %w", err)
	}
	name := slugify(firstLine(content)) + ".md"
	if err := os.WriteFile(filepath.Join(inbox, name), []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write inbox file: %w", err)
	}
	return o.delegate(ctx, "/ingest")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "inbox-item"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
```

Note: `slugify` strips non-ASCII (Cyrillic) — that is acceptable; the human-readable title stays inside the file, the slug is only the filename. If the first line is all Cyrillic, the filename falls back to `inbox-item.md`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tooling/builtin/ -run TestObsidian -v`
Expected: PASS for all `TestObsidian*` tests.

- [ ] **Step 5: Run the full package and vet**

Run: `go test ./internal/tooling/builtin/ && go vet ./internal/tooling/builtin/`
Expected: ok, no vet warnings.

- [ ] **Step 6: Commit**

```bash
git add internal/tooling/builtin/obsidian.go internal/tooling/builtin/obsidian_test.go
git commit -m "feat(obsidian): ingest writes inbox file and delegates to /ingest"
```

---

### Task 9: Register the tool (instance-gated) and document config

**Files:**
- Modify: `cmd/assistant/main.go`
- Modify: `configs/config.example.yaml`

- [ ] **Step 1: Add gated registration in `main.go`**

In `cmd/assistant/main.go`, immediately after the `run_code` registration line
(`registry.Register(builtin.NewRunCode(codeExecutor, cfg.Code.DefaultDir))`), add:

```go
	if cfg.Obsidian.VaultDir != "" {
		registry.Register(builtin.NewObsidian(cfg.Obsidian.VaultDir, codeExecutor))
		slog.Info("obsidian tool enabled", "vault_dir", cfg.Obsidian.VaultDir)
	}
```

(`codeExecutor` is the same `*claudecode.Executor` already passed to `NewRunCode`; it satisfies `builtin.CodeExecutor`.)

- [ ] **Step 2: Document the config section**

In `configs/config.example.yaml`, add (near the `code:` section):

```yaml
# Obsidian vault integration — set vault_dir to enable the `obsidian` tool.
# Leave empty to disable (e.g. on instances that should not see the vault).
obsidian:
  vault_dir: ""   # absolute path to the synced vault, e.g. /root/Obsidian/Oleg
```

- [ ] **Step 3: Verify build and full test suite**

Run: `go build ./... && go test ./...`
Expected: builds and all tests pass.

- [ ] **Step 4: Manual gating check**

Run: `go run ./cmd/assistant --config=configs/config.example.yaml 2>&1 | grep -i obsidian || echo "obsidian disabled (expected: vault_dir empty)"`
Expected: prints `obsidian disabled ...` (the example config has an empty `vault_dir`, so the tool is NOT registered). Stop the process after the line appears.

- [ ] **Step 5: Commit**

```bash
git add cmd/assistant/main.go configs/config.example.yaml
git commit -m "feat(obsidian): register tool only when obsidian.vault_dir is set"
```

---

## Phase 3 — Linode deploy + wiring (prod, step-by-step with confirmation)

> These steps touch the production bot host and a live `system-prompt.md`. Do them one at a time and confirm with the user before each server-changing step. The Go change is behind the config gate, so until step 3 sets `vault_dir`, nothing changes for the running bot.

### Task 10: Confirm prerequisites on the Linode host

- [ ] **Step 1: Confirm Claude Code is installed and authed on the bot host**

SSH to the bot host and run: `claude --version && claude -p "say ok"`
Expected: a version string and an `ok` response. If auth is missing, the user must run `claude` interactively (suggest they use `! ssh ...` then `claude` login) before proceeding.

- [ ] **Step 2: Note the bot's run path and home dir**

Confirm where the bot process runs and which user owns it (decides the vault clone path, e.g. `/root/Obsidian/Oleg`).

### Task 11: Clone the vault on Linode with git-sync

- [ ] **Step 1: Clone the vault** to the chosen path on the bot host (same git remote as the Mac vault).
- [ ] **Step 2: Add a sync cron** mirroring `~/Obsidian/Oleg/scripts/vault-sync.sh` (pull + commit + push on an interval) so Phase-1 commands and generated reports stay current both directions.
- [ ] **Step 3: Verify** the cloned vault contains `.claude/commands/weekly-review.md` (proves Phase 1 propagated via git).

### Task 12: Enable the module for Oleg only

- [ ] **Step 1: Set `obsidian.vault_dir`** in Oleg's config (`200_Проекты/Go-Assistant/configs-private/oleg/config.yaml`) to the cloned vault path. Do NOT touch Yuri's config.
- [ ] **Step 2: Rebuild/redeploy** the bot. In the logs confirm `obsidian tool enabled vault_dir=...`.
- [ ] **Step 3: Verify gating** — Yuri's instance logs do NOT show the obsidian line, and its tool list has no `obsidian`.
- [ ] **Step 4: Smoke test from Telegram** — ask `@debil4bot` to search the vault for a known term; confirm it returns a real note via the `obsidian` `search` action.

### Task 13: Seed the weekly-review cron job

- [ ] **Step 1: Add a bot cron job** (via Telegram `manage_cron`) named `weekly-review`, schedule `пт 17:00` (bot's timezone), with a prompt instructing the bot to call the `obsidian` tool with `action: weekly_review` and relay the result to chat.
- [ ] **Step 2: Trigger it manually once** and confirm: the report file appears in the vault (`_agent/reports/`) and the summary is posted to Telegram.

### Task 14: Add ingest + memory rules to the system prompt

- [ ] **Step 1: Edit `configs-private/oleg/system-prompt.md`** (Oleg only) to add two rules:
  - *Ingest-by-forward:* when the user forwards an article / PDF / voice and asks to save it, extract the text (existing vision/voice/PDF tooling) and call `obsidian` with `action: ingest` and that text as `content`.
  - *Vault-as-memory:* for questions about the user's personal notes/projects/decisions, call `obsidian` `search`/`read` before answering.
- [ ] **Step 2: Redeploy** (system prompt is loaded at startup) and verify in logs the prompt updated.
- [ ] **Step 3: End-to-end test** — forward a test article to `@debil4bot` and ask to save it; confirm a note lands in `300_Области/Источники/` and the inbox is cleared.

---

## Self-Review notes

- **Spec coverage:** Phase 1 commands (Tasks 2–4) ↔ spec §Phase 1; `obsidian` tool actions search/read/ingest/weekly_review/cross_pollinate (Tasks 6–8) ↔ spec §Phase 2 table; config + gating (Tasks 5, 9) ↔ spec §"Config & gating"; deploy/cron/system-prompt (Tasks 10–14) ↔ spec §Phase 3. All covered.
- **Type consistency:** `CodeExecutor.ExecuteJSON(ctx, prompt, workDir, onProgress)` reused verbatim from `run_code.go`; `NewObsidian(vaultDir, executor)`, `obsidianParams` fields, and `delegate` signature are consistent across Tasks 6–9. `codeExecutor` var in `main.go` matches the existing `NewRunCode` call site.
- **Placeholders:** none — every code step shows full code; Phase 3 is intentionally an ops checklist (server-side, not unit-testable), each step with a concrete verify.
- **Known tradeoff:** `slugify` drops Cyrillic from filenames only (title preserved in file body); acceptable per Task 8 note.
