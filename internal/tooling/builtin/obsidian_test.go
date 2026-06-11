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
