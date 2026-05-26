package builtin

import (
	"context"
	"encoding/json"
	"fmt"
)

type CodeExecutor interface {
	ExecuteJSON(ctx context.Context, prompt string, workDir string, onProgress func(string)) (json.RawMessage, error)
}

type RunCode struct {
	executor       CodeExecutor
	defaultWorkDir string
}

func NewRunCode(executor CodeExecutor, defaultWorkDir string) *RunCode {
	return &RunCode{
		executor:       executor,
		defaultWorkDir: defaultWorkDir,
	}
}

func (r *RunCode) Name() string        { return "run_code" }
func (r *RunCode) Description() string { return "Run Claude Code to write, fix, or refactor code in a project" }
func (r *RunCode) Category() string    { return "coding" }

func (r *RunCode) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": {
				"type": "string",
				"description": "What to code: describe the task, bug to fix, or feature to add"
			},
			"work_dir": {
				"type": "string",
				"description": "Working directory for the coding task. Defaults to the configured project directory."
			}
		},
		"required": ["prompt"]
	}`)
}

type runCodeParams struct {
	Prompt  string `json:"prompt"`
	WorkDir string `json:"work_dir"`
}

func (r *RunCode) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p runCodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	workDir := p.WorkDir
	if workDir == "" {
		workDir = r.defaultWorkDir
	}

	result, err := r.executor.ExecuteJSON(ctx, p.Prompt, workDir, nil)
	if err != nil {
		return nil, fmt.Errorf("execute code: %w", err)
	}

	return result, nil
}
