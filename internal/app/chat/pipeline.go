package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Pipeline struct {
	classifier *RuleClassifier
	llm        output.LLMProvider
	registry   output.ToolRegistry
	executor   *ToolLoop
}

func NewPipeline(
	classifier *RuleClassifier,
	llm output.LLMProvider,
	registry output.ToolRegistry,
	executor *ToolLoop,
) *Pipeline {
	return &Pipeline{
		classifier: classifier,
		llm:        llm,
		registry:   registry,
		executor:   executor,
	}
}

type ToolLoop struct {
	registry output.ToolRegistry
	maxTurns int
}

func NewToolLoop(registry output.ToolRegistry, maxTurns int) *ToolLoop {
	return &ToolLoop{registry: registry, maxTurns: maxTurns}
}

func (p *Pipeline) Process(ctx context.Context, messages []output.LLMMessage, onUpdate func(string)) (*output.LLMResponse, error) {
	lastMsg := messages[len(messages)-1].Content

	route, toolNames, confidence := p.classifier.Classify(lastMsg)
	slog.Info("classified message", "route", route, "tools", toolNames, "confidence", confidence)

	if onUpdate != nil {
		onUpdate("Analyzing...")
	}

	// Check if any message has images — use vision model
	hasImages := false
	for _, m := range messages {
		if len(m.Images) > 0 {
			hasImages = true
			break
		}
	}

	req := output.LLMRequest{
		Messages:    messages,
		MaxTokens:   4096,
		Temperature: 0.7,
	}

	if hasImages {
		req.Model = "openai/gpt-4o-mini" // vision-capable, cheap
	}

	if len(toolNames) > 0 {
		// Classifier identified specific tools
		schemas, err := p.registry.LoadSchemas(toolNames)
		if err != nil {
			slog.Warn("failed to load tool schemas", "error", err)
		} else {
			req.Tools = schemas
		}
	} else {
		// Unknown intent — give LLM all tool schemas so it can decide
		// This costs more tokens but ensures tools are always available
		allTools := p.registry.ListTools()
		allNames := make([]string, len(allTools))
		for i, t := range allTools {
			allNames[i] = t.Name
		}
		schemas, err := p.registry.LoadSchemas(allNames)
		if err != nil {
			slog.Warn("failed to load all schemas", "error", err)
		} else {
			req.Tools = schemas
		}
	}

	resp, err := p.llm.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}

	if len(resp.ToolCalls) > 0 && p.executor != nil {
		return p.executor.Run(ctx, p.llm, messages, resp, onUpdate)
	}

	return resp, nil
}

func (tl *ToolLoop) Run(
	ctx context.Context,
	llm output.LLMProvider,
	messages []output.LLMMessage,
	initialResp *output.LLMResponse,
	onUpdate func(string),
) (*output.LLMResponse, error) {
	resp := initialResp

	for turn := 0; turn < tl.maxTurns && len(resp.ToolCalls) > 0; turn++ {
		messages = append(messages, output.LLMMessage{
			Role:      entity.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			tool, err := tl.registry.GetTool(tc.Name)
			if err != nil {
				messages = append(messages, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    fmt.Sprintf("Error: tool %q not found", tc.Name),
					ToolCallID: tc.ID,
				})
				continue
			}

			if onUpdate != nil {
				onUpdate(fmt.Sprintf("Running tool: %s", tc.Name))
			}

			result, err := tool.Execute(ctx, json.RawMessage(tc.Args))
			if err != nil {
				messages = append(messages, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    fmt.Sprintf("Error: %v", err),
					ToolCallID: tc.ID,
				})
				continue
			}

			// Truncate tool results to prevent LLM overload
			resultStr := string(result)
			if len(resultStr) > 8000 {
				resultStr = resultStr[:8000] + "\n\n... (truncated, full content too large)"
			}

			messages = append(messages, output.LLMMessage{
				Role:       entity.RoleTool,
				Content:    resultStr,
				ToolCallID: tc.ID,
			})
		}

		// Deduplicate tool names
		seen := make(map[string]bool)
		var uniqueToolNames []string
		for _, tc := range resp.ToolCalls {
			if !seen[tc.Name] {
				seen[tc.Name] = true
				uniqueToolNames = append(uniqueToolNames, tc.Name)
			}
		}
		schemas, _ := tl.registry.LoadSchemas(uniqueToolNames)

		req := output.LLMRequest{
			Messages:    messages,
			Tools:       schemas,
			MaxTokens:   4096,
			Temperature: 0.7,
		}

		if onUpdate != nil {
			onUpdate("Processing results...")
		}

		var err error
		resp, err = llm.Chat(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("llm chat (tool loop turn %d): %w", turn, err)
		}
	}

	return resp, nil
}
