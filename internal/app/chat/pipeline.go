package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Pipeline struct {
	classifier  *RuleClassifier
	llm         output.LLMProvider
	visionLLM   output.LLMProvider
	registry    output.ToolRegistry
	executor    *ToolLoop
	visionModel string
	chatConfig  ChatConfig
}

type ChatConfig struct {
	MaxTokens          int
	MaxToolResultChars int
	ChatTemperature    float64
	ToolTemperature    float64
}

func NewPipeline(
	classifier *RuleClassifier,
	llm output.LLMProvider,
	visionLLM output.LLMProvider,
	registry output.ToolRegistry,
	executor *ToolLoop,
	visionModel string,
	chatConfig ChatConfig,
) *Pipeline {
	if visionModel == "" {
		visionModel = "google/gemini-2.5-flash"
	}
	// Vision runs on its own provider (OpenRouter) even when chat is on another
	// gateway. Fall back to the chat client if no dedicated vision client is given.
	if visionLLM == nil {
		visionLLM = llm
	}
	return &Pipeline{
		classifier:  classifier,
		llm:         llm,
		visionLLM:   visionLLM,
		registry:    registry,
		executor:    executor,
		visionModel: visionModel,
		chatConfig:  chatConfig,
	}
}

type ToolLoop struct {
	registry           output.ToolRegistry
	maxTurns           int
	maxToolResultChars int
	maxTokens          int
}

func NewToolLoop(registry output.ToolRegistry, maxTurns, maxToolResultChars int) *ToolLoop {
	return &ToolLoop{
		registry:           registry,
		maxTurns:           maxTurns,
		maxToolResultChars: maxToolResultChars,
		maxTokens:          4096,
	}
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
	temperature := p.chatConfig.ChatTemperature
	if len(toolNames) > 0 {
		temperature = p.chatConfig.ToolTemperature
	}

	req := output.LLMRequest{
		Messages:    messages,
		MaxTokens:   p.chatConfig.MaxTokens,
		Temperature: temperature,
	}

	// Image requests go to the vision model on its own provider (visionLLM);
	// everything else uses the primary chat client.
	activeLLM := p.llm
	if hasImages {
		req.Model = p.visionModel // vision-capable model (configurable)
		activeLLM = p.visionLLM
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

	resp, err := activeLLM.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}

	if len(resp.ToolCalls) > 0 && p.executor != nil {
		p.executor.maxTokens = p.chatConfig.MaxTokens
		resp, err = p.executor.Run(ctx, activeLLM, messages, resp, req.Tools, temperature, onUpdate)
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

		// Execute all tool calls in parallel — independent calls (e.g. multiple
		// search_web or read_pdf) run concurrently instead of sequentially.
		type toolResult struct {
			idx     int
			message output.LLMMessage
		}
		results := make([]toolResult, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.ToolCalls {
			wg.Add(1)
			go func(i int, tc entity.ToolCall) {
				defer wg.Done()
				tool, err := tl.registry.GetTool(tc.Name)
				if err != nil {
					results[i] = toolResult{i, output.LLMMessage{
						Role:       entity.RoleTool,
						Content:    fmt.Sprintf("Error: tool %q not found", tc.Name),
						ToolCallID: tc.ID,
					}}
					return
				}

				if onUpdate != nil {
					onUpdate(fmt.Sprintf("Running tool: %s", tc.Name))
				}

				slog.Info("tool loop: executing tool", "turn", turn, "tool", tc.Name)
				result, err := tool.Execute(ctx, json.RawMessage(tc.Args))
				if err != nil {
					slog.Warn("tool loop: tool error", "tool", tc.Name, "error", err)
					results[i] = toolResult{i, output.LLMMessage{
						Role:       entity.RoleTool,
						Content:    fmt.Sprintf("Error: %v", err),
						ToolCallID: tc.ID,
					}}
					return
				}

				// Truncate tool results to prevent LLM overload. Smart truncation:
				// keep beginning (context) + end (conclusion) so key info survives.
				resultStr := string(result)
				if len(resultStr) > tl.maxToolResultChars {
					head := tl.maxToolResultChars * 2 / 3
					tail := tl.maxToolResultChars - head - 100
					truncated := len(resultStr) - tl.maxToolResultChars
					resultStr = resultStr[:head] +
						fmt.Sprintf("\n\n... [truncated %d chars] ...\n\n", truncated) +
						resultStr[len(resultStr)-tail:]
				}

				results[i] = toolResult{i, output.LLMMessage{
					Role:       entity.RoleTool,
					Content:    resultStr,
					ToolCallID: tc.ID,
				}}
			}(i, tc)
		}
		wg.Wait()
		for _, r := range results {
			messages = append(messages, r.message)
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
			MaxTokens:   tl.maxTokens,
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

	// Safety net: if the loop ended without text (model kept calling tools to
	// the budget, or the final tools-less turn returned nothing), force one more
	// call that asks for an answer from what was gathered, so the user never
	// gets an empty reply.
	if resp.Content == "" {
		slog.Warn("tool loop: ended with empty answer, forcing final summary")
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
		}
		messages = append(messages, output.LLMMessage{
			Role:    entity.RoleUser,
			Content: "Хватит вызывать инструменты. На основе уже собранных данных дай финальный ответ на русском прямо сейчас. Если каких-то данных не хватило — честно перечисли, что осталось непроверенным.",
		})
		if final, err := llm.Chat(ctx, output.LLMRequest{
			Messages:    messages,
			MaxTokens:   tl.maxTokens,
			Temperature: temperature,
		}); err == nil && final.Content != "" {
			resp = final
		}
	}

	return resp, nil
}
