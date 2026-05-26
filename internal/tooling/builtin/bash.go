package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Bash struct{}

func NewBash() *Bash {
	return &Bash{}
}

func (b *Bash) Name() string        { return "bash" }
func (b *Bash) Description() string { return "Execute a shell command on the server and return output" }
func (b *Bash) Category() string    { return "system" }

func (b *Bash) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "Timeout in seconds (default 30, max 120)",
				"default": 30
			}
		},
		"required": ["command"]
	}`)
}

type bashParams struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type bashResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func (b *Bash) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p bashParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Block dangerous commands
	lower := strings.ToLower(p.Command)
	blocked := []string{"rm -rf /", "mkfs", "dd if=", "> /dev/sd", "shutdown", "reboot", "init 0", "init 6"}
	for _, b := range blocked {
		if strings.Contains(lower, b) {
			return json.Marshal(bashResult{
				Stderr:   "command blocked: potentially destructive",
				ExitCode: 1,
			})
		}
	}

	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", p.Command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := bashResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
			result.Stderr = err.Error()
		}
	}

	// Truncate long output
	if len(result.Stdout) > 4000 {
		result.Stdout = result.Stdout[:4000] + "\n... (truncated)"
	}
	if len(result.Stderr) > 2000 {
		result.Stderr = result.Stderr[:2000] + "\n... (truncated)"
	}

	return json.Marshal(result)
}
