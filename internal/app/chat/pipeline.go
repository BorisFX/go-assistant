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
	classifier  *RuleClassifier
	llm         output.LLMProvider
	synthLLM    output.LLMProvider
	registry    output.ToolRegistry
	executor    *ToolLoop
	visionModel string
}

func NewPipeline(
	classifier *RuleClassifier,
	llm output.LLMProvider,
	synthLLM output.LLMProvider,
	registry output.ToolRegistry,
	executor *ToolLoop,
	visionModel string,
) *Pipeline {
	if visionModel == "" {
		visionModel = "google/gemini-2.5-flash"
	}
	return &Pipeline{
		classifier:  classifier,
		llm:         llm,
		synthLLM:    synthLLM,
		registry:    registry,
		executor:    executor,
		visionModel: visionModel,
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

	// Tool-driven work (document analysis, signatures, trading, code) is a
	// factual task: keep temperature low so the model reports what it read
	// instead of confabulating. Plain chat stays conversational.
	temperature := 0.7
	if len(toolNames) > 0 {
		temperature = 0.2
	}

	req := output.LLMRequest{
		Messages:    messages,
		MaxTokens:   4096,
		Temperature: temperature,
	}

	if hasImages {
		req.Model = p.visionModel // vision-capable model (configurable)
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
		resp, err = p.executor.Run(ctx, p.llm, p.synthLLM, messages, resp, req.Tools, temperature, onUpdate)
		if err != nil {
			return nil, err
		}
	}

	// Some models occasionally return an empty completion (no text, no tool call).
	// Surface a clear message rather than letting an empty reply reach the user.
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		slog.Warn("empty pipeline response, returning fallback")
		resp.Content = "Что-то пошло не так — модель вернула пустой ответ. Попробуй переформулировать или повторить запрос."
	}

	return resp, nil
}

func (tl *ToolLoop) Run(
	ctx context.Context,
	llm output.LLMProvider,
	synthLLM output.LLMProvider,
	messages []output.LLMMessage,
	initialResp *output.LLMResponse,
	tools []entity.ToolDefinition,
	temperature float64,
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

			slog.Info("tool loop: executing tool", "turn", turn, "tool", tc.Name)
			result, err := tool.Execute(ctx, json.RawMessage(tc.Args))
			if err != nil {
				slog.Warn("tool loop: tool error", "tool", tc.Name, "error", err)
				messages = append(messages, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    fmt.Sprintf("Error: %v", err),
					ToolCallID: tc.ID,
				})
				continue
			}

			// Truncate tool results to prevent LLM overload. Document tools
			// (read_pdf) can return up to 40k chars; keep enough that a single
			// page of a document survives instead of being cut to a stub.
			resultStr := string(result)
			if len(resultStr) > 24000 {
				resultStr = resultStr[:24000] + "\n\n... (truncated, full content too large)"
			}

			messages = append(messages, output.LLMMessage{
				Role:       entity.RoleTool,
				Content:    resultStr,
				ToolCallID: tc.ID,
			})
		}

		// Make the model aware the tool budget is finite so it converges on an
		// answer instead of investigating until it runs out. Escalate the warning
		// as the limit approaches.
		if remaining := tl.maxTurns - turn - 1; remaining <= 3 {
			messages = append(messages, output.LLMMessage{
				Role:    entity.RoleSystem,
				Content: fmt.Sprintf("Внимание: осталось %d вызовов инструментов из %d. Заканчивай сбор данных и переходи к финальному текстовому ответу на русском — на последнем шаге инструменты будут недоступны.", remaining, tl.maxTurns),
			})
		}

		// On the final allowed turn, drop the tools so the model is forced to
		// produce a text answer instead of requesting yet another tool call —
		// otherwise hitting maxTurns yields an empty response to the user.
		turnTools := tools
		if turn == tl.maxTurns-1 {
			turnTools = nil
		}

		// Keep the full route toolset available every turn so the model can
		// chain different tools (e.g. inspect_signature after read_pdf) instead
		// of being limited to the tools it already called.
		req := output.LLMRequest{
			Messages:    messages,
			Tools:       turnTools,
			MaxTokens:   4096,
			Temperature: temperature,
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

	// Final synthesis. When a dedicated (pricier) synthesis model is configured,
	// the cheap model drives every tool-calling turn and the expensive model is
	// invoked exactly once to write the conclusion from the gathered context.
	// Without a synthesis model, we still force a final pass when the loop ended
	// empty, so the user never gets a blank reply.
	needFinal := synthLLM != nil || resp.Content == ""
	if needFinal {
		finalLLM := llm
		if synthLLM != nil {
			finalLLM = synthLLM
			slog.Info("tool loop: handing final answer to synthesis model")
		} else {
			slog.Warn("tool loop: ended with empty answer, forcing final summary")
		}

		// If the loop ended on an unanswered tool request (hit the turn budget),
		// close those calls out with placeholders so the message history stays
		// valid (assistant tool_calls must be followed by tool results).
		if len(resp.ToolCalls) > 0 {
			messages = append(messages, output.LLMMessage{
				Role:      entity.RoleAssistant,
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			})
			for _, tc := range resp.ToolCalls {
				messages = append(messages, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    "(пропущено: лимит вызовов инструментов исчерпан)",
					ToolCallID: tc.ID,
				})
			}
		} else if resp.Content != "" {
			// The cheap model already produced a draft answer; keep it in the
			// transcript so the synthesis model refines rather than restarts.
			messages = append(messages, output.LLMMessage{
				Role:    entity.RoleAssistant,
				Content: resp.Content,
			})
		}

		messages = append(messages, output.LLMMessage{
			Role:    entity.RoleUser,
			Content: "Хватит вызывать инструменты. На основе уже собранных данных дай финальный ответ на русском прямо сейчас. Если каких-то данных не хватило — честно перечисли, что осталось непроверенным.",
		})

		if onUpdate != nil {
			onUpdate("Formulating answer...")
		}

		if final, err := finalLLM.Chat(ctx, output.LLMRequest{
			Messages:    messages,
			MaxTokens:   4096,
			Temperature: temperature,
		}); err != nil {
			slog.Warn("tool loop: final synthesis failed", "error", err)
		} else if final.Content != "" {
			resp = final
		}
	}

	return resp, nil
}
