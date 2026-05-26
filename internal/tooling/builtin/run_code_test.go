package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type mockCodeExecutor struct {
	lastPrompt  string
	lastWorkDir string
}

func (m *mockCodeExecutor) ExecuteJSON(ctx context.Context, prompt string, workDir string, onProgress func(string)) (json.RawMessage, error) {
	m.lastPrompt = prompt
	m.lastWorkDir = workDir
	return json.RawMessage(`{"output":"done","exit_code":0}`), nil
}

func TestRunCodeTool_Metadata(t *testing.T) {
	tool := builtin.NewRunCode(&mockCodeExecutor{}, "/default/dir")

	if tool.Name() != "run_code" {
		t.Errorf("expected name run_code, got %s", tool.Name())
	}
	if tool.Category() != "coding" {
		t.Errorf("expected category coding, got %s", tool.Category())
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["prompt"]; !ok {
		t.Error("schema missing 'prompt' property")
	}
}

func TestRunCodeTool_Execute(t *testing.T) {
	mock := &mockCodeExecutor{}
	tool := builtin.NewRunCode(mock, "/default/dir")

	params := json.RawMessage(`{"prompt":"fix the bug","work_dir":"/custom/dir"}`)
	result, err := tool.Execute(context.Background(), params)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.lastPrompt != "fix the bug" {
		t.Errorf("expected prompt 'fix the bug', got %s", mock.lastPrompt)
	}

	if mock.lastWorkDir != "/custom/dir" {
		t.Errorf("expected work_dir '/custom/dir', got %s", mock.lastWorkDir)
	}

	var output map[string]any
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestRunCodeTool_Execute_DefaultWorkDir(t *testing.T) {
	mock := &mockCodeExecutor{}
	tool := builtin.NewRunCode(mock, "/default/dir")

	params := json.RawMessage(`{"prompt":"write tests"}`)
	tool.Execute(context.Background(), params)

	if mock.lastWorkDir != "/default/dir" {
		t.Errorf("expected default work_dir '/default/dir', got %s", mock.lastWorkDir)
	}
}
