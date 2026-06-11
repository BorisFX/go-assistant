package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
