# Claude-Code-style Agent Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the assistant's tool loop behave like Claude Code — a single strong model (Sonnet 4.6) that explores files and drives many tool turns affordably via Anthropic prompt caching, with file-exploration tools and isolated subagents.

**Architecture:** Two phases. **Phase 1 (high value, low risk)** fixes the real problems: add prompt caching to the OpenRouter client so the re-sent tool-loop context costs ~0.1×, revert the DeepSeek dual-model hack, and put Sonnet 4.6 back as the single driver. This alone kills the $21 cost blowup and the weak-driver problem. **Phase 2 (agent layer)** adds Claude-Code-style tools: `read_file` / `grep` / `glob` over a sandboxed workspace, a `task` subagent tool with isolated context, and an in-conversation `plan` checklist tool.

**Tech Stack:** Go 1.24, OpenRouter chat-completions API, Anthropic `cache_control` ephemeral breakpoints, ripgrep (`rg`) for grep, existing `tooling.Registry` + `output.Tool` interface.

**Ship Phase 1 independently.** Phase 2 is additive and can be deferred if Phase 1 already restores quality.

---

## File Structure

| File | Responsibility | Phase |
|------|----------------|-------|
| `internal/adapter/driven/openrouter/client.go` | Add `cache_control` breakpoints to request body; parse cache usage | 1 |
| `internal/adapter/driven/openrouter/client_test.go` | Test cache breakpoint placement + Anthropic gating | 1 |
| `internal/app/chat/pipeline.go` | Revert dual-model split; single driver; keep turn warnings + safety net | 1 |
| `cmd/assistant/main.go` | Drop synthLLM wiring | 1 |
| `pkg/config/config.go` | Remove `Synthesis` field (or leave documented-but-unused) | 1 |
| Server `config.yaml` (both bots) | `chat.model: anthropic/claude-sonnet-4.6` | 1 |
| `internal/tooling/builtin/read_file.go` (+test) | Read a workspace file with line numbers, offset/limit | 2 |
| `internal/tooling/builtin/grep.go` (+test) | ripgrep-backed content search, line numbers | 2 |
| `internal/tooling/builtin/glob.go` (+test) | Glob file paths, sorted by mtime | 2 |
| `internal/tooling/builtin/fsutil.go` (+test) | Shared workspace-root resolution + path-traversal guard | 2 |
| `internal/tooling/builtin/task_agent.go` (+test) | `task` subagent: isolated tool loop, returns summary | 2 |
| `internal/tooling/builtin/plan.go` (+test) | In-memory per-conversation checklist | 2 |
| `cmd/assistant/main.go` | Register new tools | 2 |

---

## PHASE 1 — Caching + revert + driver swap

### Task 1: Prompt caching in the OpenRouter client

**Files:**
- Modify: `internal/adapter/driven/openrouter/client.go`
- Test: `internal/adapter/driven/openrouter/client_test.go`

**Context:** `BuildRequestBody` (client.go:142) serializes `req.Messages` to `[]APIMessage`. Content is a plain `string` today except when images are present (then `[]ContentPart`). Anthropic caching needs `cache_control: {type: ephemeral}` attached to a content *part*, so cached messages must use the array form. A breakpoint caches everything *before and including* that part. We place breakpoints on the stable prefix so growing tool results at the tail pay full price once, then become cached next turn. Caching only works for Anthropic models via OpenRouter, so gate on `anthropic/` prefix. Max 4 breakpoints; min cacheable prefix ~1024 tokens (smaller is silently ignored — safe). JSON field order must stay stable: we use structs (deterministic), so this is fine.

- [ ] **Step 1: Write the failing test**

Add to `client_test.go`:

```go
func TestBuildRequestBody_CachesAnthropicSystemAndPrefix(t *testing.T) {
	req := output.LLMRequest{
		Messages: []output.LLMMessage{
			{Role: entity.RoleSystem, Content: "You are an assistant."},
			{Role: entity.RoleUser, Content: "first"},
			{Role: entity.RoleAssistant, Content: "answer"},
			{Role: entity.RoleUser, Content: "second"},
		},
	}
	body := BuildRequestBody("anthropic/claude-sonnet-4.6", req)

	// System message must be in content-array form with a cache breakpoint.
	sysParts, ok := body.Messages[0].Content.([]ContentPart)
	if !ok {
		t.Fatalf("system content = %T, want []ContentPart", body.Messages[0].Content)
	}
	if sysParts[len(sysParts)-1].CacheControl == nil {
		t.Error("system message missing cache_control breakpoint")
	}

	// The last message of the prior turn (index len-2, the assistant) gets the
	// advancing breakpoint so the next turn reads it from cache.
	last := body.Messages[len(body.Messages)-2]
	parts, ok := last.Content.([]ContentPart)
	if !ok || parts[len(parts)-1].CacheControl == nil {
		t.Error("trailing-stable message missing cache_control breakpoint")
	}
}

func TestBuildRequestBody_NoCacheForNonAnthropic(t *testing.T) {
	req := output.LLMRequest{Messages: []output.LLMMessage{
		{Role: entity.RoleSystem, Content: "sys"},
		{Role: entity.RoleUser, Content: "hi"},
	}}
	body := BuildRequestBody("deepseek/deepseek-v4-pro", req)
	if _, isArray := body.Messages[0].Content.([]ContentPart); isArray {
		t.Error("non-anthropic model should keep plain string content (no cache parts)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/driven/openrouter/ -run TestBuildRequestBody_Cache -v`
Expected: FAIL — `CacheControl` field undefined / breakpoints absent.

- [ ] **Step 3: Add the CacheControl type and field**

In `client.go`, extend `ContentPart`:

```go
type ContentPart struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ImageURL     *ImageURL     `json:"image_url,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}
```

- [ ] **Step 4: Apply breakpoints in BuildRequestBody**

In `BuildRequestBody`, after the message loop builds `body.Messages` and before `return body`, add Anthropic-only caching. Replace the final `return body` with:

```go
	applyCaching(model, &body)
	return body
}

// applyCaching marks the stable request prefix with Anthropic ephemeral cache
// breakpoints so the tool loop re-reads it at ~0.1x instead of full price.
// No-op for non-Anthropic models (OpenRouter ignores/rejects cache_control there).
func applyCaching(model string, body *RequestBody) {
	if !strings.HasPrefix(model, "anthropic/") {
		return
	}
	// Breakpoint 1: end of the last tool definition (tools rarely change).
	if n := len(body.Tools); n > 0 {
		body.Tools[n-1].CacheControl = &CacheControl{Type: "ephemeral"}
	}
	// Breakpoint 2: the first system message (stable across the whole loop).
	for i := range body.Messages {
		if body.Messages[i].Role == "system" {
			cacheMessage(&body.Messages[i])
			break
		}
	}
	// Breakpoint 3: the last message of the *previous* turn (the second-to-last
	// message overall). The current turn's fresh tool results sit after it and
	// pay full price once, then fold into the cached prefix next turn.
	if n := len(body.Messages); n >= 2 {
		cacheMessage(&body.Messages[n-2])
	}
}

// cacheMessage converts a message's content to the array form (if needed) and
// attaches a cache breakpoint to its final text part.
func cacheMessage(m *APIMessage) {
	switch c := m.Content.(type) {
	case string:
		if c == "" {
			return // empty parts can't be cached
		}
		m.Content = []ContentPart{{Type: "text", Text: c, CacheControl: &CacheControl{Type: "ephemeral"}}}
	case []ContentPart:
		if len(c) == 0 {
			return
		}
		c[len(c)-1].CacheControl = &CacheControl{Type: "ephemeral"}
		m.Content = c
	}
}
```

Add `CacheControl *CacheControl` to `APITool`:

```go
type APITool struct {
	Type         string        `json:"type"`
	Function     APIFunction   `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapter/driven/openrouter/ -run TestBuildRequestBody -v`
Expected: PASS (both new tests and existing ones).

- [ ] **Step 6: Parse and log cache usage**

Extend `Usage` and log cache hits in `Chat`. In `client.go`:

```go
type Usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}
```

In `Chat`, after building `result`, add:

```go
	if apiResp.Usage.PromptTokensDetails.CachedTokens > 0 {
		slog.Info("llm cache hit",
			"model", apiResp.Model,
			"cached_tokens", apiResp.Usage.PromptTokensDetails.CachedTokens,
			"prompt_tokens", apiResp.Usage.PromptTokens)
	}
```

- [ ] **Step 7: Build + commit**

```bash
go build ./... && go test ./internal/adapter/driven/openrouter/...
git add internal/adapter/driven/openrouter/client.go internal/adapter/driven/openrouter/client_test.go
git commit -m "feat(openrouter): add Anthropic prompt caching to cut tool-loop cost"
```

---

### Task 2: Revert the dual-model split

**Files:**
- Modify: `internal/app/chat/pipeline.go`
- Modify: `cmd/assistant/main.go`
- Modify: `pkg/config/config.go`

**Context:** Commit `df18dbd` added a `synthLLM` second model threaded through `Pipeline` → `Process` → `ToolLoop.Run`, plus turn-limit warnings, a broadened empty-answer safety net, and `maxTurns=25`. We keep the warnings, safety net, and maxTurns (they're good), but remove the second-model split: with cached Sonnet, one model both drives and concludes. The synthesis tail becomes "force a final answer only when the loop ended empty" (the pre-`df18dbd` behavior).

- [ ] **Step 1: Remove synthLLM from the Pipeline struct and constructor**

In `pipeline.go`, restore:

```go
type Pipeline struct {
	classifier  *RuleClassifier
	llm         output.LLMProvider
	registry    output.ToolRegistry
	executor    *ToolLoop
	visionModel string
}

func NewPipeline(
	classifier *RuleClassifier,
	llm output.LLMProvider,
	registry output.ToolRegistry,
	executor *ToolLoop,
	visionModel string,
) *Pipeline {
	if visionModel == "" {
		visionModel = "google/gemini-2.5-flash"
	}
	return &Pipeline{
		classifier:  classifier,
		llm:         llm,
		registry:    registry,
		executor:    executor,
		visionModel: visionModel,
	}
}
```

- [ ] **Step 2: Restore the Run call site and signature**

`Process` (~line 116):
```go
		resp, err = p.executor.Run(ctx, p.llm, messages, resp, req.Tools, temperature, onUpdate)
```

`Run` signature: drop the `synthLLM` parameter, back to:
```go
func (tl *ToolLoop) Run(
	ctx context.Context,
	llm output.LLMProvider,
	messages []output.LLMMessage,
	initialResp *output.LLMResponse,
	tools []entity.ToolDefinition,
	temperature float64,
	onUpdate func(string),
) (*output.LLMResponse, error) {
```

- [ ] **Step 3: Restore the Run tail to the single-model safety net**

Replace the `needFinal := synthLLM != nil || ...` block with the empty-only safety net (keep the warnings block earlier in the loop untouched):

```go
	// Safety net: if the loop ended without text (model kept calling tools to
	// the budget, or the final tools-less turn returned nothing), force one more
	// call that asks for an answer from what was gathered, so the user never
	// gets an empty reply.
	if resp.Content == "" {
		slog.Warn("tool loop: ended with empty answer, forcing final summary")
		if len(resp.ToolCalls) > 0 {
			messages = append(messages, output.LLMMessage{
				Role:      entity.RoleAssistant,
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			})
			for _, tc := range resp.ToolCalls {
				messages = append(messages, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    "(пропущено: лимит вызовов инструментов исчерпан)",
					ToolCallID: tc.ID,
				})
			}
		}
		messages = append(messages, output.LLMMessage{
			Role:    entity.RoleUser,
			Content: "Хватит вызывать инструменты. На основе уже собранных данных дай финальный ответ на русском прямо сейчас. Если каких-то данных не хватило — честно перечисли, что осталось непроверенным.",
		})
		if final, err := llm.Chat(ctx, output.LLMRequest{
			Messages:    messages,
			MaxTokens:   4096,
			Temperature: temperature,
		}); err == nil && final.Content != "" {
			resp = final
		}
	}

	return resp, nil
}
```

- [ ] **Step 4: Drop synthLLM wiring in main.go**

Remove the `var synthLLM output.LLMProvider { ... }` block (~lines 99-110) and the `output` import if it becomes unused. Restore the call:
```go
	pipeline := chat.NewPipeline(classifier, llmClient, registry, toolLoop, cfg.LLM.Vision.Model)
```

- [ ] **Step 5: Remove the Synthesis config field**

In `config.go`, remove the `Synthesis LLMModel` field and its comment from the `LLM` struct. Remove the `synthesis:` block from `configs/config.example.yaml`.

- [ ] **Step 6: Build + commit**

```bash
go build ./... && go test ./internal/app/chat/...
git add internal/app/chat/pipeline.go cmd/assistant/main.go pkg/config/config.go configs/config.example.yaml
git commit -m "revert(chat): drop dual-model split; single cached Sonnet driver"
```

---

### Task 3: Switch both bots to Sonnet 4.6 and deploy

**Files:** Server `config.yaml` for `assistant` and `assistant-yuri`.

- [ ] **Step 1: Set chat model on the server (with backup)**

For each config (`/opt/assistant/config.yaml`, `/opt/assistant-yuri/config.yaml`): back it up timestamped, set `chat.model: "anthropic/claude-sonnet-4.6"`, `chat.fallback: "anthropic/claude-sonnet-4.5"`, and remove the now-unused `synthesis:` block. Use the same Python-on-server edit pattern as before (deterministic, keeps indentation).

- [ ] **Step 2: Deploy**

Run: `make deploy`
Expected: both services `active`; Yuri log shows no `synthesis model enabled` line (removed).

- [ ] **Step 3: Verify caching live (after credits are topped up)**

Send one document/tool message to each bot. Run on server:
`journalctl -u assistant-yuri -n 200 --no-pager | grep "llm cache hit"`
Expected: `cached_tokens` > 0 on turns after the first. **Blocked until the OpenRouter account is funded** (currently drained at 202.04/202).

---

## PHASE 2 — Claude-Code-style agent layer

**Workspace root:** all filesystem tools operate under a single sandbox root = `cfg.Code.DefaultDir` (the same dir `run_code` uses). Paths are resolved against it and a traversal guard rejects anything escaping it. `bash` stays unrestricted; these tools are the *safe, structured* exploration surface the model prefers.

### Task 4: Shared workspace path guard

**Files:**
- Create: `internal/tooling/builtin/fsutil.go`
- Test: `internal/tooling/builtin/fsutil_test.go`

- [ ] **Step 1: Write the failing test**

```go
package builtin

import (
	"path/filepath"
	"testing"
)

func TestResolveInWorkspace(t *testing.T) {
	root := t.TempDir()
	ok, err := resolveInWorkspace(root, "sub/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok != filepath.Join(root, "sub/file.txt") {
		t.Errorf("got %q", ok)
	}
	if _, err := resolveInWorkspace(root, "../escape"); err == nil {
		t.Error("expected traversal to be rejected")
	}
	if _, err := resolveInWorkspace(root, "/etc/passwd"); err == nil {
		t.Error("expected absolute path outside root to be rejected")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestResolveInWorkspace -v`
Expected: FAIL — `resolveInWorkspace` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"fmt"
	"path/filepath"
	"strings"
)

// resolveInWorkspace joins a user-supplied relative path onto the workspace root
// and rejects anything that escapes it (path traversal / absolute outside root).
func resolveInWorkspace(root, rel string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(cleanRoot, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", rel)
	}
	return abs, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestResolveInWorkspace -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/fsutil.go internal/tooling/builtin/fsutil_test.go
git commit -m "feat(tools): workspace path-traversal guard for file tools"
```

### Task 5: read_file tool

**Files:**
- Create: `internal/tooling/builtin/read_file.go`
- Test: `internal/tooling/builtin/read_file_test.go`

**Context:** Like Claude Code's Read — returns content with `cat -n`-style line numbers so the model can refer back without re-reading. Supports `offset`/`limit` for large files; default cap 400 lines. Follow the `bash.go` tool shape (Name/Description/Category/Schema/Execute + `NewReadFile(root string)`).

- [ ] **Step 1: Write the failing test**

```go
func TestReadFile_NumbersLines(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644)
	tool := NewReadFile(root)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "1\talpha") || !strings.Contains(s, "3\tgamma") {
		t.Errorf("missing numbered lines: %s", s)
	}
}

func TestReadFile_RejectsEscape(t *testing.T) {
	tool := NewReadFile(t.TempDir())
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../../etc/passwd"}`)); err == nil {
		t.Error("expected traversal rejection")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestReadFile -v`
Expected: FAIL — `NewReadFile` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type ReadFile struct{ root string }

func NewReadFile(root string) *ReadFile { return &ReadFile{root: root} }

func (r *ReadFile) Name() string        { return "read_file" }
func (r *ReadFile) Description() string { return "Read a file from the workspace, returned with line numbers. Use offset/limit to page large files." }
func (r *ReadFile) Category() string    { return "files" }

func (r *ReadFile) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Path relative to the workspace root"},
			"offset":{"type":"integer","description":"1-based first line to read (default 1)"},
			"limit":{"type":"integer","description":"Max lines to read (default 400)"}
		},
		"required":["path"]
	}`)
}

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (r *ReadFile) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p readFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	abs, err := resolveInWorkspace(r.root, p.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	if p.Offset <= 0 {
		p.Offset = 1
	}
	if p.Limit <= 0 || p.Limit > 2000 {
		p.Limit = 400
	}

	var sb strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	written := 0
	for sc.Scan() {
		line++
		if line < p.Offset {
			continue
		}
		if written >= p.Limit {
			fmt.Fprintf(&sb, "\n... (truncated at line %d; call again with offset=%d)\n", line, line)
			break
		}
		fmt.Fprintf(&sb, "%d\t%s\n", line, sc.Text())
		written++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return json.Marshal(map[string]string{"content": sb.String()})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestReadFile -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/read_file.go internal/tooling/builtin/read_file_test.go
git commit -m "feat(tools): add read_file with line numbers and paging"
```

### Task 6: grep tool

**Files:**
- Create: `internal/tooling/builtin/grep.go`
- Test: `internal/tooling/builtin/grep_test.go`

**Context:** ripgrep-backed (`rg` is installed on the server; `bash.go` proves `exec` is available). Output modes: `files_with_matches` (default) and `content` (with `-n` line numbers). Always run with the workspace root as cwd, and pass the pattern as a single arg (no shell interpolation — use `exec.CommandContext("rg", args...)` directly, never `bash -c`, to avoid injection).

- [ ] **Step 1: Write the failing test**

```go
func TestGrep_FindsContentWithLineNumbers(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "x.go"), []byte("package main\nfunc Foo(){}\n"), 0o644)
	tool := NewGrep(root)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"func Foo","mode":"content"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "x.go") || !strings.Contains(string(out), "2:") {
		t.Errorf("expected file+line match, got: %s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestGrep -v`
Expected: FAIL — `NewGrep` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Grep struct{ root string }

func NewGrep(root string) *Grep { return &Grep{root: root} }

func (g *Grep) Name() string        { return "grep" }
func (g *Grep) Description() string { return "Search workspace file contents with ripgrep. mode=files_with_matches (default) or content (line numbers)." }
func (g *Grep) Category() string    { return "files" }

func (g *Grep) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"pattern":{"type":"string","description":"Regex pattern (ripgrep syntax)"},
			"glob":{"type":"string","description":"Optional file glob filter, e.g. *.go"},
			"mode":{"type":"string","enum":["files_with_matches","content"],"description":"Output mode (default files_with_matches)"}
		},
		"required":["pattern"]
	}`)
}

type grepParams struct {
	Pattern string `json:"pattern"`
	Glob    string `json:"glob"`
	Mode    string `json:"mode"`
}

func (g *Grep) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p grepParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	args := []string{"--color=never"}
	if p.Mode == "content" {
		args = append(args, "-n")
	} else {
		args = append(args, "-l")
	}
	if p.Glob != "" {
		args = append(args, "--glob", p.Glob)
	}
	args = append(args, "-e", p.Pattern, ".")

	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = g.root
	out, err := cmd.CombinedOutput()
	// rg exits 1 when there are no matches — that is not an error for us.
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return json.Marshal(map[string]string{"result": "(no matches)"})
		}
		return nil, fmt.Errorf("rg: %v: %s", err, string(out))
	}
	s := string(out)
	if len(s) > 8000 {
		s = s[:8000] + "\n... (truncated)"
	}
	return json.Marshal(map[string]string{"result": strings.TrimRight(s, "\n")})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestGrep -v`
Expected: PASS (skip gracefully if `rg` missing — see note). If `rg` is not installed locally, the test will fail to find the binary; guard with `if _, err := exec.LookPath("rg"); err != nil { t.Skip("rg not installed") }` at the top of the test.

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/grep.go internal/tooling/builtin/grep_test.go
git commit -m "feat(tools): add ripgrep-backed grep over workspace"
```

### Task 7: glob tool

**Files:**
- Create: `internal/tooling/builtin/glob.go`
- Test: `internal/tooling/builtin/glob_test.go`

**Context:** Returns matching paths (relative to root) sorted newest-first by mtime, capped at 100, like Claude Code's Glob. Use `filepath.WalkDir` + `path.Match` on `**`-flattened patterns; simplest correct approach: walk the tree and match each path's base or full relative path with `doublestar`-style semantics. To avoid a new dependency, support `**/` prefix by matching the suffix glob against the base name, and a plain glob against the relative path.

- [ ] **Step 1: Write the failing test**

```go
func TestGlob_MatchesAndSortsByMtime(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	older := filepath.Join(root, "pkg", "a.go")
	newer := filepath.Join(root, "pkg", "b.go")
	os.WriteFile(older, []byte("x"), 0o644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(newer, []byte("x"), 0o644)

	tool := NewGlob(root)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"**/*.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Paths []string `json:"paths"` }
	json.Unmarshal(out, &res)
	if len(res.Paths) != 2 || !strings.HasSuffix(res.Paths[0], "b.go") {
		t.Errorf("expected b.go first (newest), got %v", res.Paths)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestGlob -v`
Expected: FAIL — `NewGlob` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Glob struct{ root string }

func NewGlob(root string) *Glob { return &Glob{root: root} }

func (g *Glob) Name() string        { return "glob" }
func (g *Glob) Description() string { return "List workspace files matching a glob pattern (supports **/ prefix), newest first." }
func (g *Glob) Category() string    { return "files" }

func (g *Glob) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{"pattern":{"type":"string","description":"Glob, e.g. **/*.go or src/*.ts"}},
		"required":["pattern"]
	}`)
}

type globParams struct {
	Pattern string `json:"pattern"`
}

func (g *Glob) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p globParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	recursive := strings.HasPrefix(p.Pattern, "**/")
	suffix := strings.TrimPrefix(p.Pattern, "**/")

	type entry struct {
		rel  string
		mod  int64
	}
	var matches []entry
	filepath.WalkDir(g.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(g.root, path)
		var ok bool
		if recursive {
			ok, _ = filepath.Match(suffix, filepath.Base(rel))
		} else {
			ok, _ = filepath.Match(p.Pattern, rel)
		}
		if ok {
			if info, e := os.Stat(path); e == nil {
				matches = append(matches, entry{rel: rel, mod: info.ModTime().UnixNano()})
			}
		}
		return nil
	})
	sort.Slice(matches, func(i, j int) bool { return matches[i].mod > matches[j].mod })
	if len(matches) > 100 {
		matches = matches[:100]
	}
	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = m.rel
	}
	return json.Marshal(map[string][]string{"paths": paths})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestGlob -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/glob.go internal/tooling/builtin/glob_test.go
git commit -m "feat(tools): add glob file listing (newest first)"
```

### Task 8: task subagent tool

**Files:**
- Create: `internal/tooling/builtin/task_agent.go`
- Test: `internal/tooling/builtin/task_agent_test.go`

**Context:** A `task` tool that runs an isolated tool loop: fresh message history (system + the task prompt only — no parent conversation), the same registry **minus `task` itself** (no recursion), its own LLM provider, and returns a single text summary. This protects the parent's context from verbose intermediate tool output. Reuses `chat.ToolLoop` and an `output.LLMProvider`. To keep `builtin` free of an import cycle with `chat`, the tool depends only on `output.LLMProvider`, `output.ToolRegistry`, and runs a minimal inline loop (it does NOT import `chat`). Model can be a cheaper one (e.g. Gemini Flash) passed in at construction.

Constructor: `NewTaskAgent(llm output.LLMProvider, registry output.ToolRegistry, maxTurns int, systemPrompt string)`. Execute: build `[]output.LLMMessage{{system}, {user: task}}`, load all schemas except `task`, run up to `maxTurns` turns mirroring `ToolLoop.Run`'s execute-tools-then-Chat cycle, return final `Content`.

- [ ] **Step 1: Write the failing test (with a fake LLM)**

```go
type fakeLLM struct{ replies []*output.LLMResponse; i int }

func (f *fakeLLM) Chat(ctx context.Context, req output.LLMRequest) (*output.LLMResponse, error) {
	r := f.replies[f.i]
	f.i++
	return r, nil
}
func (f *fakeLLM) ChatStream(ctx context.Context, req output.LLMRequest, on func(string)) (*output.LLMResponse, error) {
	return f.Chat(ctx, req)
}

func TestTaskAgent_ReturnsSummary(t *testing.T) {
	reg := tooling.NewRegistry()
	llm := &fakeLLM{replies: []*output.LLMResponse{{Content: "done: 3 files"}}}
	tool := NewTaskAgent(llm, reg, 5, "You are a sub-agent.")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"count files"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "done: 3 files") {
		t.Errorf("got %s", out)
	}
}
```

(Place the test in `package builtin`; it imports `tooling` and `output`. If that creates a cycle, move `fakeLLM` and the test to `builtin_test` external package.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestTaskAgent -v`
Expected: FAIL — `NewTaskAgent` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type TaskAgent struct {
	llm          output.LLMProvider
	registry     output.ToolRegistry
	maxTurns     int
	systemPrompt string
}

func NewTaskAgent(llm output.LLMProvider, registry output.ToolRegistry, maxTurns int, systemPrompt string) *TaskAgent {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	return &TaskAgent{llm: llm, registry: registry, maxTurns: maxTurns, systemPrompt: systemPrompt}
}

func (t *TaskAgent) Name() string        { return "task" }
func (t *TaskAgent) Description() string { return "Delegate a self-contained sub-task (e.g. exploring files, gathering data) to an isolated agent. Returns only its final summary, keeping this conversation's context clean." }
func (t *TaskAgent) Category() string    { return "agent" }

func (t *TaskAgent) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{"task":{"type":"string","description":"Self-contained instruction for the sub-agent. Include all context it needs."}},
		"required":["task"]
	}`)
}

type taskParams struct {
	Task string `json:"task"`
}

func (t *TaskAgent) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p taskParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	// All tools except this one, to prevent recursive task spawning.
	var names []string
	for _, s := range t.registry.ListTools() {
		if s.Name != t.Name() {
			names = append(names, s.Name)
		}
	}
	schemas, _ := t.registry.LoadSchemas(names)

	messages := []output.LLMMessage{
		{Role: entity.RoleSystem, Content: t.systemPrompt},
		{Role: entity.RoleUser, Content: p.Task},
	}

	resp, err := t.llm.Chat(ctx, output.LLMRequest{Messages: messages, Tools: schemas, MaxTokens: 4096, Temperature: 0.2})
	if err != nil {
		return nil, err
	}

	for turn := 0; turn < t.maxTurns && len(resp.ToolCalls) > 0; turn++ {
		messages = append(messages, output.LLMMessage{Role: entity.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			tool, err := t.registry.GetTool(tc.Name)
			if err != nil {
				messages = append(messages, output.LLMMessage{Role: entity.RoleTool, Content: "Error: tool not found", ToolCallID: tc.ID})
				continue
			}
			result, err := tool.Execute(ctx, json.RawMessage(tc.Args))
			content := string(result)
			if err != nil {
				content = "Error: " + err.Error()
			}
			if len(content) > 16000 {
				content = content[:16000] + "\n... (truncated)"
			}
			messages = append(messages, output.LLMMessage{Role: entity.RoleTool, Content: content, ToolCallID: tc.ID})
		}
		turnTools := schemas
		if turn == t.maxTurns-1 {
			turnTools = nil
		}
		resp, err = t.llm.Chat(ctx, output.LLMRequest{Messages: messages, Tools: turnTools, MaxTokens: 4096, Temperature: 0.2})
		if err != nil {
			return nil, err
		}
	}

	return json.Marshal(map[string]string{"summary": resp.Content})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestTaskAgent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/task_agent.go internal/tooling/builtin/task_agent_test.go
git commit -m "feat(tools): add task subagent with isolated context"
```

### Task 9: plan (todo checklist) tool

**Files:**
- Create: `internal/tooling/builtin/plan.go`
- Test: `internal/tooling/builtin/plan_test.go`

**Context:** A lightweight in-memory checklist the model maintains across turns of one run to stay coherent on multi-step tasks (Claude Code's TaskCreate role). Single shared instance per process is acceptable for a single-owner bot; store items as `[]planItem{Text, Done}` guarded by a mutex. Actions: `set` (replace the whole list), `check` (mark item index done), `list`.

- [ ] **Step 1: Write the failing test**

```go
func TestPlan_SetCheckList(t *testing.T) {
	tool := NewPlan()
	tool.Execute(context.Background(), json.RawMessage(`{"action":"set","items":["read","write","verify"]}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"check","index":0}`))
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	s := string(out)
	if !strings.Contains(s, "[x] read") || !strings.Contains(s, "[ ] write") {
		t.Errorf("unexpected list: %s", s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tooling/builtin/ -run TestPlan -v`
Expected: FAIL — `NewPlan` undefined.

- [ ] **Step 3: Implement**

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type planItem struct {
	Text string
	Done bool
}

type Plan struct {
	mu    sync.Mutex
	items []planItem
}

func NewPlan() *Plan { return &Plan{} }

func (p *Plan) Name() string        { return "plan" }
func (p *Plan) Description() string { return "Maintain a task checklist for a multi-step job. action=set (replace items), check (mark index done), list." }
func (p *Plan) Category() string    { return "agent" }

func (p *Plan) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","enum":["set","check","list"]},
			"items":{"type":"array","items":{"type":"string"}},
			"index":{"type":"integer"}
		},
		"required":["action"]
	}`)
}

type planParams struct {
	Action string   `json:"action"`
	Items  []string `json:"items"`
	Index  int      `json:"index"`
}

func (p *Plan) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var pr planParams
	if err := json.Unmarshal(params, &pr); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	switch pr.Action {
	case "set":
		p.items = p.items[:0]
		for _, it := range pr.Items {
			p.items = append(p.items, planItem{Text: it})
		}
	case "check":
		if pr.Index >= 0 && pr.Index < len(p.items) {
			p.items[pr.Index].Done = true
		}
	case "list":
		// fallthrough to render
	default:
		return nil, fmt.Errorf("unknown action %q", pr.Action)
	}

	var sb strings.Builder
	for i, it := range p.items {
		mark := " "
		if it.Done {
			mark = "x"
		}
		fmt.Fprintf(&sb, "%d. [%s] %s\n", i, mark, it.Text)
	}
	return json.Marshal(map[string]string{"plan": sb.String()})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/tooling/builtin/ -run TestPlan -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tooling/builtin/plan.go internal/tooling/builtin/plan_test.go
git commit -m "feat(tools): add plan checklist tool"
```

### Task 10: Register the new tools and update the system prompt

**Files:**
- Modify: `cmd/assistant/main.go`
- Modify: server `system-prompt.md` / `system-prompt-yuri.md` (tool list + exploration guidance)

- [ ] **Step 1: Register tools in main.go**

After the existing `registry.Register(...)` calls (~line 105-118), add:

```go
	workspace := cfg.Code.DefaultDir
	registry.Register(builtin.NewReadFile(workspace))
	registry.Register(builtin.NewGrep(workspace))
	registry.Register(builtin.NewGlob(workspace))
	registry.Register(builtin.NewPlan())
	registry.Register(builtin.NewTaskAgent(llmClient, registry, 12, systemPrompt))
```

Note: `NewTaskAgent` must be registered AFTER `systemPrompt` is loaded (move the block below the system-prompt load at ~line 137-146, or pass `defaultSystemPrompt`). Verify ordering when implementing.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Update the system prompt tool list**

Add to the `TOOLS:` line in both server prompts: `read_file, grep, glob (explore workspace files), task (delegate isolated sub-jobs), plan (track multi-step work)`. Add one guidance line: prefer `glob`/`grep`/`read_file` to locate and read files instead of `bash cat`; use `task` for verbose exploration to keep context clean.

- [ ] **Step 4: Deploy**

Run: `make deploy`
Expected: both services active; `tools` count in the `assistant ready` log increased by 5.

- [ ] **Step 5: Commit**

```bash
git add cmd/assistant/main.go
git commit -m "feat(agent): register file-exploration, task, and plan tools"
```

---

## Notes / Open Decisions

- **Credits:** No bot request succeeds until the OpenRouter account is topped up (drained at 202.04/202). Phase 1 Step 3 live verification is blocked on this.
- **Workspace root:** defaults to `cfg.Code.DefaultDir`. If you want the bot to explore a specific project, point that config at it. `bash` remains unrestricted; the new tools are the safe structured surface.
- **Subagent model:** Task 8 wires the subagent to the main `llmClient` (Sonnet). If cost matters, construct a cheaper provider (e.g. `google/gemini-2.5-flash`) and pass it to `NewTaskAgent` instead — subagents are where a cheaper model is genuinely fine, since the parent Sonnet still writes the final answer.
- **`rg` dependency:** grep shells out to ripgrep. Confirm `rg` is installed on the server (`which rg`); if not, `apt-get install -y ripgrep` before deploying Task 6.
