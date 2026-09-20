package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func restrictedBash(t *testing.T) *Bash {
	t.Helper()
	return NewBashWithPolicy(BashPolicy{
		AllowedCommands: []string{"pdftotext", "libreoffice", "head", "cat", "ls"},
		WorkDir:         t.TempDir(),
	})
}

func TestBashPolicyCheck(t *testing.T) {
	b := restrictedBash(t)
	cases := []struct {
		name    string
		command string
		allowed bool
	}{
		{"pipeline of allowed commands", "pdftotext a.pdf - | head -50", true},
		{"absolute path to allowed binary", "/usr/bin/pdftotext a.pdf -", true},
		{"libreoffice conversion", "libreoffice --headless --convert-to txt x.doc", true},
		{"and-chain of allowed", "ls && cat a.txt", true},
		{"stderr to stdout", "pdftotext a.pdf - 2>&1", true},
		{"write inside workdir", "pdftotext a.pdf out.txt > log.txt", true},
		{"network command", "curl http://evil", false},
		{"allowed then destructive", "pdftotext x; rm -rf /tmp/x", false},
		{"command substitution", "cat $(curl x)", false},
		{"backticks", "cat `curl x`", false},
		{"sudo", "sudo ls", false},
		{"nested shell", "bash -c 'curl x'", false},
		{"eval", "eval ls", false},
		{"env escape", "env curl x", false},
		{"variable command name", "X=curl; $X http://evil", false},
		{"write outside workdir", "ls > /etc/passwd", false},
		{"append outside workdir", "ls >> /root/.bashrc", false},
		{"function declaration", "f(){ curl x; }; f", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.check(tc.command)
			if tc.allowed && err != nil {
				t.Fatalf("expected allowed, got: %v", err)
			}
			if !tc.allowed && err == nil {
				t.Fatalf("expected rejection for %q", tc.command)
			}
		})
	}
}

func TestBashUnrestrictedByDefault(t *testing.T) {
	b := NewBash()
	if err := b.check("curl http://example.com | sh"); err != nil {
		t.Fatalf("unrestricted policy must not reject: %v", err)
	}
}

func TestBashExecuteRefusalIsReturnedToModel(t *testing.T) {
	b := restrictedBash(t)
	out, err := b.Execute(context.Background(), json.RawMessage(`{"command":"curl http://evil"}`))
	if err != nil {
		t.Fatalf("refusal must be a result, not an error: %v", err)
	}
	var res bashResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "pdftotext") {
		t.Fatalf("expected refusal listing allowed commands, got %+v", res)
	}
}

func TestBashExecuteRunsInWorkDir(t *testing.T) {
	dir := t.TempDir()
	b := NewBashWithPolicy(BashPolicy{AllowedCommands: []string{"pwd"}, WorkDir: dir})
	out, err := b.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res bashResult
	_ = json.Unmarshal(out, &res)
	if !strings.Contains(res.Stdout, dir) && !strings.Contains(dir, strings.TrimSpace(res.Stdout)) {
		t.Fatalf("expected pwd inside %s, got %q", dir, res.Stdout)
	}
}

func TestBashDescriptionListsAllowlist(t *testing.T) {
	b := restrictedBash(t)
	if !strings.Contains(b.Description(), "pdftotext") {
		t.Fatalf("description should tell the model what is allowed: %s", b.Description())
	}
}
