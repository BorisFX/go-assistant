package subagent

import (
	"context"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// Config describes one isolated subagent run: which model answers, the system
// prompt that frames its job, the subset of tools it may call, and its budgets.
type Config struct {
	Model        string
	SystemPrompt string
	ToolNames    []string // allowed tool subset; empty = no tools
	MaxTurns     int
	Temperature  float64
	MaxTokens    int
}

const (
	defaultMaxTurns  = 8
	defaultMaxTokens = 4096
)

// Runner executes a single-purpose subagent: a fresh, isolated loop with its own
// model and a restricted toolset that returns only its final text.
type Runner struct {
	llm      output.LLMProvider
	registry output.ToolRegistry
}

func NewRunner(llm output.LLMProvider, registry output.ToolRegistry) *Runner {
	return &Runner{llm: llm, registry: registry}
}

// Run starts a subagent on task and returns only its final text. The
// conversation is built from scratch (system prompt + task) so the caller's
// context never leaks in.
func (r *Runner) Run(ctx context.Context, cfg Config, task string) (string, error) {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaultMaxTurns
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}

	messages := []output.LLMMessage{
		{Role: entity.RoleSystem, Content: cfg.SystemPrompt},
		{Role: entity.RoleUser, Content: task},
	}

	resp, err := r.llm.Chat(ctx, output.LLMRequest{
		Messages:    messages,
		Model:       cfg.Model,
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
	})
	if err != nil {
		return "", fmt.Errorf("subagent llm chat: %w", err)
	}
	return resp.Content, nil
}
