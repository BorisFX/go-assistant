package subagent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling"
)

// fakeLLM returns scripted responses in order and records each request received.
type fakeLLM struct {
	responses []*output.LLMResponse
	calls     []output.LLMRequest
}

func (f *fakeLLM) Chat(_ context.Context, req output.LLMRequest) (*output.LLMResponse, error) {
	f.calls = append(f.calls, req)
	i := len(f.calls) - 1
	if i >= len(f.responses) {
		return &output.LLMResponse{}, nil
	}
	return f.responses[i], nil
}

func (f *fakeLLM) ChatStream(ctx context.Context, req output.LLMRequest, _ func(string)) (*output.LLMResponse, error) {
	return f.Chat(ctx, req)
}

// fakeTool records how many times it ran and returns a fixed JSON result.
type fakeTool struct {
	name   string
	result string
	calls  int
}

func (t *fakeTool) Name() string           { return t.name }
func (t *fakeTool) Description() string     { return "fake tool " + t.name }
func (t *fakeTool) Category() string        { return "test" }
func (t *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{}}`) }
func (t *fakeTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	t.calls++
	return json.RawMessage(t.result), nil
}

func newRegistry(t *testing.T, tools ...output.Tool) *tooling.Registry {
	t.Helper()
	r := tooling.NewRegistry()
	for _, tl := range tools {
		if err := r.Register(tl); err != nil {
			t.Fatalf("register %s: %v", tl.Name(), err)
		}
	}
	return r
}

func TestRunnerReturnsFinalTextNoTools(t *testing.T) {
	llm := &fakeLLM{responses: []*output.LLMResponse{{Content: "digest text"}}}
	r := subagent.NewRunner(llm, newRegistry(t))

	got, err := r.Run(context.Background(), subagent.Config{
		Model:        "deepseek/deepseek-v4-flash",
		SystemPrompt: "You summarize.",
		Temperature:  0.2,
		MaxTokens:    1000,
	}, "summarize this document")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "digest text" {
		t.Errorf("got %q, want %q", got, "digest text")
	}
	if len(llm.calls) != 1 {
		t.Fatalf("want 1 llm call, got %d", len(llm.calls))
	}
	req := llm.calls[0]
	if req.Model != "deepseek/deepseek-v4-flash" {
		t.Errorf("model: got %q", req.Model)
	}
	if len(req.Tools) != 0 {
		t.Errorf("want no tools, got %d", len(req.Tools))
	}
	if len(req.Messages) != 2 ||
		req.Messages[0].Role != entity.RoleSystem || req.Messages[0].Content != "You summarize." ||
		req.Messages[1].Role != entity.RoleUser || req.Messages[1].Content != "summarize this document" {
		t.Errorf("unexpected messages: %+v", req.Messages)
	}
}

func TestRunnerLoadsOnlyAllowedToolSubset(t *testing.T) {
	alpha := &fakeTool{name: "alpha", result: `{"ok":true}`}
	beta := &fakeTool{name: "beta", result: `{"ok":true}`}
	llm := &fakeLLM{responses: []*output.LLMResponse{{Content: "done"}}}
	r := subagent.NewRunner(llm, newRegistry(t, alpha, beta))

	_, err := r.Run(context.Background(), subagent.Config{
		Model:     "m",
		ToolNames: []string{"alpha"},
	}, "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := llm.calls[0]
	if len(req.Tools) != 1 || req.Tools[0].Name != "alpha" {
		t.Errorf("want only alpha tool, got %+v", req.Tools)
	}
}

func TestRunnerErrorsOnUnknownTool(t *testing.T) {
	llm := &fakeLLM{responses: []*output.LLMResponse{{Content: "done"}}}
	r := subagent.NewRunner(llm, newRegistry(t))

	_, err := r.Run(context.Background(), subagent.Config{
		Model:     "m",
		ToolNames: []string{"missing"},
	}, "task")
	if err == nil {
		t.Fatal("want error for unknown tool, got nil")
	}
}

func TestRunnerExecutesToolThenReturnsFinalText(t *testing.T) {
	alpha := &fakeTool{name: "alpha", result: `{"value":42}`}
	llm := &fakeLLM{responses: []*output.LLMResponse{
		{ToolCalls: []entity.ToolCall{{ID: "c1", Name: "alpha", Args: "{}"}}},
		{Content: "final digest"},
	}}
	r := subagent.NewRunner(llm, newRegistry(t, alpha))

	got, err := r.Run(context.Background(), subagent.Config{
		Model:     "m",
		ToolNames: []string{"alpha"},
		MaxTurns:  5,
	}, "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "final digest" {
		t.Errorf("got %q, want %q", got, "final digest")
	}
	if alpha.calls != 1 {
		t.Errorf("want alpha executed once, got %d", alpha.calls)
	}
	second := llm.calls[1]
	foundResult := false
	for _, m := range second.Messages {
		if m.Role == entity.RoleTool && m.ToolCallID == "c1" && m.Content == `{"value":42}` {
			foundResult = true
		}
	}
	if !foundResult {
		t.Errorf("tool result not fed back to model: %+v", second.Messages)
	}
}

func TestRunnerRefusesDisallowedTool(t *testing.T) {
	alpha := &fakeTool{name: "alpha", result: `{"ok":true}`}
	bash := &fakeTool{name: "bash", result: `{"ran":true}`}
	llm := &fakeLLM{responses: []*output.LLMResponse{
		{ToolCalls: []entity.ToolCall{{ID: "c1", Name: "bash", Args: "{}"}}},
		{Content: "answer"},
	}}
	r := subagent.NewRunner(llm, newRegistry(t, alpha, bash))

	got, err := r.Run(context.Background(), subagent.Config{
		Model:     "m",
		ToolNames: []string{"alpha"}, // bash registered globally but NOT granted here
		MaxTurns:  5,
	}, "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "answer" {
		t.Errorf("got %q, want %q", got, "answer")
	}
	if bash.calls != 0 {
		t.Errorf("disallowed bash tool must not run, ran %d times", bash.calls)
	}
	second := llm.calls[1]
	refused := false
	for _, m := range second.Messages {
		if m.Role == entity.RoleTool && m.ToolCallID == "c1" && strings.Contains(m.Content, "not permitted") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("expected refusal message fed back, got %+v", second.Messages)
	}
}

func TestRunnerDropsToolsOnFinalTurn(t *testing.T) {
	alpha := &fakeTool{name: "alpha", result: `{"ok":true}`}
	// Model tries to call a tool every turn; the 2-turn budget must force an answer.
	llm := &fakeLLM{responses: []*output.LLMResponse{
		{ToolCalls: []entity.ToolCall{{ID: "c1", Name: "alpha", Args: "{}"}}},
		{Content: "forced final answer"},
	}}
	r := subagent.NewRunner(llm, newRegistry(t, alpha))

	got, err := r.Run(context.Background(), subagent.Config{
		Model:     "m",
		ToolNames: []string{"alpha"},
		MaxTurns:  2,
	}, "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "forced final answer" {
		t.Errorf("got %q, want %q", got, "forced final answer")
	}
	if len(llm.calls) != 2 {
		t.Fatalf("want exactly 2 llm calls, got %d", len(llm.calls))
	}
	if len(llm.calls[1].Tools) != 0 {
		t.Errorf("final turn must withdraw tools, got %d", len(llm.calls[1].Tools))
	}
}
