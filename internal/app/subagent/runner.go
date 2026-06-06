package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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
	toolResultLimit  = 24000
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

	tools, err := r.loadTools(cfg.ToolNames)
	if err != nil {
		return "", err
	}

	messages := []output.LLMMessage{
		{Role: entity.RoleSystem, Content: cfg.SystemPrompt},
		{Role: entity.RoleUser, Content: task},
	}

	var resp *output.LLMResponse
	for turn := 0; turn < cfg.MaxTurns; turn++ {
		resp, err = r.llm.Chat(ctx, output.LLMRequest{
			Messages:    messages,
			Tools:       tools,
			Model:       cfg.Model,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
		})
		if err != nil {
			return "", fmt.Errorf("subagent llm chat (turn %d): %w", turn, err)
		}
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		messages = append(messages, output.LLMMessage{
			Role:      entity.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		for _, tc := range resp.ToolCalls {
			messages = append(messages, r.runTool(ctx, tc))
		}
	}
	return resp.Content, nil
}

// loadTools resolves the granted tool names to their schemas. An unknown name is
// a configuration error — fail loudly rather than silently dropping the tool.
func (r *Runner) loadTools(names []string) ([]entity.ToolDefinition, error) {
	if len(names) == 0 {
		return nil, nil
	}
	defs, err := r.registry.LoadSchemas(names)
	if err != nil {
		return nil, fmt.Errorf("load subagent tools: %w", err)
	}
	return defs, nil
}

// runTool executes one tool call and returns the tool message to feed back to
// the model. Tool errors are reported to the model (not returned as Go errors)
// so the subagent can recover instead of aborting the whole run.
func (r *Runner) runTool(ctx context.Context, tc entity.ToolCall) output.LLMMessage {
	tool, err := r.registry.GetTool(tc.Name)
	if err != nil {
		return output.LLMMessage{
			Role:       entity.RoleTool,
			Content:    fmt.Sprintf("Error: tool %q not found", tc.Name),
			ToolCallID: tc.ID,
		}
	}

	result, err := tool.Execute(ctx, json.RawMessage(tc.Args))
	if err != nil {
		slog.Warn("subagent: tool error", "tool", tc.Name, "error", err)
		return output.LLMMessage{
			Role:       entity.RoleTool,
			Content:    fmt.Sprintf("Error: %v", err),
			ToolCallID: tc.ID,
		}
	}

	resultStr := string(result)
	if len(resultStr) > toolResultLimit {
		resultStr = resultStr[:toolResultLimit] + "\n\n... (truncated, full content too large)"
	}
	return output.LLMMessage{
		Role:       entity.RoleTool,
		Content:    resultStr,
		ToolCallID: tc.ID,
	}
}
