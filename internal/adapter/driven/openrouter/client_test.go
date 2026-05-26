package openrouter_test

import (
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func TestBuildRequestBody_BasicChat(t *testing.T) {
	req := output.LLMRequest{
		Messages: []output.LLMMessage{
			{Role: entity.RoleSystem, Content: "You are a helpful assistant."},
			{Role: entity.RoleUser, Content: "Hello"},
		},
		MaxTokens:   1024,
		Temperature: 0.7,
	}

	body := openrouter.BuildRequestBody("deepseek/deepseek-v4-flash", req)

	if body.Model != "deepseek/deepseek-v4-flash" {
		t.Errorf("expected model deepseek/deepseek-v4-flash, got %s", body.Model)
	}

	if len(body.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(body.Messages))
	}

	if body.Messages[0].Role != "system" {
		t.Errorf("expected role system, got %s", body.Messages[0].Role)
	}

	if body.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %d", body.MaxTokens)
	}
}

func TestBuildRequestBody_WithToolNames(t *testing.T) {
	req := output.LLMRequest{
		Messages: []output.LLMMessage{
			{Role: entity.RoleUser, Content: "Search for Go jobs"},
		},
		ToolNames: []entity.ToolSummary{
			{Name: "search_web", Description: "Search the web"},
			{Name: "trading_status", Description: "Get trading status"},
		},
	}

	body := openrouter.BuildRequestBody("deepseek/deepseek-v4-flash", req)

	if len(body.Messages) != 2 {
		t.Fatalf("expected 2 messages (system with tool names + user), got %d", len(body.Messages))
	}
}

func TestBuildRequestBody_WithToolDefinitions(t *testing.T) {
	req := output.LLMRequest{
		Messages: []output.LLMMessage{
			{Role: entity.RoleUser, Content: "Search for Go jobs"},
		},
		Tools: []entity.ToolDefinition{
			{
				ToolSummary: entity.ToolSummary{Name: "search_web", Description: "Search the web"},
				Schema:      []byte(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			},
		},
	}

	body := openrouter.BuildRequestBody("deepseek/deepseek-v4-flash", req)

	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(body.Tools))
	}

	if body.Tools[0].Function.Name != "search_web" {
		t.Errorf("expected tool name search_web, got %s", body.Tools[0].Function.Name)
	}
}
