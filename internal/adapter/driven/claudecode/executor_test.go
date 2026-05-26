package claudecode_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/claudecode"
)

func TestNewExecutor_DefaultConfig(t *testing.T) {
	exec := claudecode.New("/tmp/workdir", "claude")

	if exec.Binary() != "claude" {
		t.Errorf("expected binary 'claude', got %s", exec.Binary())
	}

	if exec.WorkDir() != "/tmp/workdir" {
		t.Errorf("expected workdir '/tmp/workdir', got %s", exec.WorkDir())
	}
}

func TestBuildArgs(t *testing.T) {
	args := claudecode.BuildArgs("fix the login bug", 20)

	expected := []string{"-p", "fix the login bug", "--output-format", "json", "--max-turns", "20"}

	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d", len(expected), len(args))
	}

	for i, arg := range args {
		if arg != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], arg)
		}
	}
}
