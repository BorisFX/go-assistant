package tooling_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/tooling"
)

type mockTool struct {
	name        string
	description string
	category    string
	schema      json.RawMessage
}

func (t *mockTool) Name() string           { return t.name }
func (t *mockTool) Description() string    { return t.description }
func (t *mockTool) Category() string       { return t.category }
func (t *mockTool) Schema() json.RawMessage { return t.schema }
func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"result":"ok"}`), nil
}

func newMockTool(name, desc, cat string) *mockTool {
	return &mockTool{
		name:        name,
		description: desc,
		category:    cat,
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "search query"}
			},
			"required": ["query"]
		}`),
	}
}

func TestRegistry_ListTools_ReturnsOnlySummaries(t *testing.T) {
	reg := tooling.NewRegistry()
	reg.Register(newMockTool("search_web", "Search the web", "search"))
	reg.Register(newMockTool("trading_status", "Get trading status", "trading"))

	tools := reg.ListTools()

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool name is empty")
		}
		if tool.Description == "" {
			t.Error("tool description is empty")
		}
	}
}

func TestRegistry_LoadSchema_ReturnsFullDefinition(t *testing.T) {
	reg := tooling.NewRegistry()
	reg.Register(newMockTool("search_web", "Search the web", "search"))

	def, err := reg.LoadSchema("search_web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if def.Name != "search_web" {
		t.Errorf("expected name search_web, got %s", def.Name)
	}

	if def.Schema == nil {
		t.Error("schema should not be nil")
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	if schema["type"] != "object" {
		t.Error("schema type should be object")
	}
}

func TestRegistry_LoadSchema_UnknownTool_ReturnsError(t *testing.T) {
	reg := tooling.NewRegistry()

	_, err := reg.LoadSchema("nonexistent")
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestRegistry_LoadSchemas_ReturnsMultiple(t *testing.T) {
	reg := tooling.NewRegistry()
	reg.Register(newMockTool("search_web", "Search", "search"))
	reg.Register(newMockTool("trading_status", "Trading", "trading"))
	reg.Register(newMockTool("bash", "Run command", "system"))

	defs, err := reg.LoadSchemas([]string{"search_web", "trading_status"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
}

func TestRegistry_GetTool_ExecutesTool(t *testing.T) {
	reg := tooling.NewRegistry()
	reg.Register(newMockTool("search_web", "Search", "search"))

	tool, err := reg.GetTool("search_web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("execution error: %v", err)
	}

	if string(result) != `{"result":"ok"}` {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestRegistry_Register_DuplicateName_ReturnsError(t *testing.T) {
	reg := tooling.NewRegistry()
	reg.Register(newMockTool("search_web", "Search", "search"))

	err := reg.Register(newMockTool("search_web", "Duplicate", "search"))
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}
