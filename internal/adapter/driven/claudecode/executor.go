package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Result struct {
	Output       string   `json:"output"`
	ExitCode     int      `json:"exit_code"`
	Error        string   `json:"error,omitempty"`
	FilesChanged []string `json:"files_changed,omitempty"`
}

type Executor struct {
	binary  string
	workDir string
}

func New(workDir, binary string) *Executor {
	if binary == "" {
		binary = "claude"
	}
	return &Executor{
		binary:  binary,
		workDir: workDir,
	}
}

func (e *Executor) Binary() string  { return e.binary }
func (e *Executor) WorkDir() string { return e.workDir }

func BuildArgs(prompt string, maxTurns int) []string {
	return []string{
		"-p", prompt,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(maxTurns),
	}
}

func (e *Executor) Execute(ctx context.Context, prompt string, workDir string, onProgress func(string)) (*Result, error) {
	if workDir == "" {
		workDir = e.workDir
	}

	args := BuildArgs(prompt, 20)
	cmd := exec.CommandContext(ctx, e.binary, args...)
	cmd.Dir = workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	var outputBuilder strings.Builder

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputBuilder.WriteString(line + "\n")
			if onProgress != nil {
				onProgress(line)
			}
		}
	}()

	var stderrBuilder strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			stderrBuilder.WriteString(scanner.Text() + "\n")
		}
	}()

	err = cmd.Wait()

	result := &Result{
		Output:   outputBuilder.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}

	if err != nil {
		result.Error = stderrBuilder.String()
		if result.Error == "" {
			result.Error = err.Error()
		}
	}

	return result, nil
}

func (e *Executor) ExecuteJSON(ctx context.Context, prompt string, workDir string, onProgress func(string)) (json.RawMessage, error) {
	result, err := e.Execute(ctx, prompt, workDir, onProgress)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	return data, nil
}
