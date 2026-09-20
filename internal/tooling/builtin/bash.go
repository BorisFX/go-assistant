package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// BashPolicy confines what the model may run. Empty AllowedCommands means the
// shell is unrestricted (the owner's own instance); a non-empty list means
// every command in the script must be on it. Mirrors config.BashPolicy so the
// tool package does not import config.
type BashPolicy struct {
	AllowedCommands []string
	WorkDir         string
}

// Restricted reports whether the policy confines commands at all.
func (p BashPolicy) Restricted() bool { return len(p.AllowedCommands) > 0 }

type Bash struct {
	policy  BashPolicy
	allowed map[string]bool
}

func NewBash() *Bash { return NewBashWithPolicy(BashPolicy{}) }

// NewBashWithPolicy builds the tool with an allowlist. The instance that reads
// documents from strangers gets pdftotext and friends, nothing that reaches
// the network or the rest of the box.
func NewBashWithPolicy(p BashPolicy) *Bash {
	allowed := make(map[string]bool, len(p.AllowedCommands))
	for _, c := range p.AllowedCommands {
		allowed[filepath.Base(strings.TrimSpace(c))] = true
	}
	return &Bash{policy: p, allowed: allowed}
}

func (b *Bash) Name() string { return "bash" }

func (b *Bash) Description() string {
	if b.policy.Restricted() {
		return "Execute a shell command on the server and return output. Only these commands are allowed: " +
			strings.Join(b.policy.AllowedCommands, ", ")
	}
	return "Execute a shell command on the server and return output"
}

func (b *Bash) Category() string { return "system" }

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
	for _, bl := range blocked {
		if strings.Contains(lower, bl) {
			return json.Marshal(bashResult{
				Stderr:   "command blocked: potentially destructive",
				ExitCode: 1,
			})
		}
	}

	if err := b.check(p.Command); err != nil {
		// A refused command is an answer for the model, not a tool failure:
		// it should pick an allowed one instead of retrying the same call.
		return json.Marshal(bashResult{Stderr: err.Error(), ExitCode: 1})
	}

	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", p.Command)
	if b.policy.WorkDir != "" {
		cmd.Dir = b.policy.WorkDir
	}

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

// alwaysDenied are shell escapes that would let an allowed word run anything.
var alwaysDenied = map[string]bool{
	"eval": true, "exec": true, "sudo": true, "su": true, "doas": true,
	"bash": true, "sh": true, "zsh": true, "dash": true, "env": true, "xargs": true,
	"command": true, "source": true, ".": true, "nohup": true, "setsid": true,
}

// check parses the script and verifies every command against the allowlist.
// Parsing rather than string-matching: "pdftotext x; curl evil" and "$(curl x)"
// both look like an allowed command to a prefix check.
func (b *Bash) check(script string) error {
	if !b.policy.Restricted() {
		return nil
	}
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return fmt.Errorf("команда не разобрана как shell-скрипт: %v", err)
	}

	var verr error
	syntax.Walk(prog, func(node syntax.Node) bool {
		if verr != nil {
			return false
		}
		switch n := node.(type) {
		case *syntax.CallExpr:
			verr = b.checkCall(n)
		case *syntax.Redirect:
			verr = b.checkRedirect(n)
		case *syntax.FuncDecl:
			verr = fmt.Errorf("объявление функций в команде запрещено")
		}
		return verr == nil
	})
	return verr
}

func (b *Bash) checkCall(call *syntax.CallExpr) error {
	if len(call.Args) == 0 {
		// Bare assignment like FOO=bar — harmless on its own.
		return nil
	}
	name, ok := literalWord(call.Args[0])
	if !ok {
		return fmt.Errorf("имя команды должно быть литералом, без подстановок; разрешены: %s", b.allowedList())
	}
	base := filepath.Base(name)
	if alwaysDenied[base] {
		return fmt.Errorf("команда %q запрещена; разрешены: %s", name, b.allowedList())
	}
	if !b.allowed[base] {
		return fmt.Errorf("команда %q не входит в список разрешённых: %s", name, b.allowedList())
	}
	return nil
}

// checkRedirect keeps writes inside WorkDir. Reads are fine: the allowed
// commands read files anyway, and the tool already returns their output.
func (b *Bash) checkRedirect(r *syntax.Redirect) error {
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.DplOut:
	default:
		return nil
	}
	if r.Op == syntax.DplOut { // 2>&1 and friends
		return nil
	}
	target, ok := literalWord(r.Word)
	if !ok {
		return fmt.Errorf("путь перенаправления должен быть литералом")
	}
	if target == "/dev/null" {
		return nil
	}
	if b.policy.WorkDir == "" {
		return nil
	}
	if !insideDir(b.policy.WorkDir, target) {
		return fmt.Errorf("запись разрешена только внутри рабочей папки %s", b.policy.WorkDir)
	}
	return nil
}

// literalWord returns the word's text when it consists only of literal parts
// (plain text or quoted strings without expansions).
func literalWord(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var sb strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(p.Value)
		case *syntax.SglQuoted:
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				sb.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func insideDir(dir, target string) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (b *Bash) allowedList() string {
	return strings.Join(b.policy.AllowedCommands, ", ")
}
