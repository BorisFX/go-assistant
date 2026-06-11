package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestObsidianReadSymlinkEscape(t *testing.T) {
	vault := writeVault(t)
	// An outside file the symlink will point to.
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the vault pointing outside it.
	inbox := filepath.Join(vault, "_agent", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inbox, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	o := builtin.NewObsidian(vault, &fakeExecutor{})
	_, err := o.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"_agent/inbox/link"}`))
	if err == nil {
		t.Error("expected symlink-escape rejection, got nil error")
	}
}

func TestObsidianSearchSnippetValidUTF8(t *testing.T) {
	dir := t.TempDir()
	// Cyrillic note; search for a Cyrillic substring deep enough that the
	// byte-offset window starts/ends mid-rune unless snapped to boundaries.
	body := "# Заметка\nЭто длинная русская заметка про блокчейн и криптовалюты в Камбодже, очень полезная.\n"
	if err := os.WriteFile(filepath.Join(dir, "ru.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	o := builtin.NewObsidian(dir, &fakeExecutor{})
	out, err := o.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"блокчейн"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Hits []struct {
			Path    string `json:"path"`
			Snippet string `json:"snippet"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("expected a hit, got none: %s", out)
	}
	for _, h := range res.Hits {
		if !utf8.ValidString(h.Snippet) {
			t.Errorf("snippet is not valid UTF-8: %q", h.Snippet)
		}
		// JSON marshaling silently replaces invalid bytes with U+FFFD; a
		// mid-rune cut would surface as the replacement char here.
		if strings.ContainsRune(h.Snippet, '�') {
			t.Errorf("snippet contains replacement char (mid-rune cut): %q", h.Snippet)
		}
		// The matched query must appear intact in the snippet.
		if !strings.Contains(h.Snippet, "блокчейн") {
			t.Errorf("snippet missing intact query: %q", h.Snippet)
		}
	}
}

func TestObsidianIngestUniqueNames(t *testing.T) {
	vault := writeVault(t)
	o := builtin.NewObsidian(vault, &fakeExecutor{})
	for i := 0; i < 2; i++ {
		if _, err := o.Execute(context.Background(), json.RawMessage(`{"action":"ingest","content":"same content"}`)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(vault, "_agent", "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 distinct inbox files, got %d", count)
	}
}

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
