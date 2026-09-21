package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// schemaRegistry records which tool schemas the pipeline asks for.
type schemaRegistry struct {
	stubRegistry
	requested []string
}

func (r *schemaRegistry) ListTools() []entity.ToolSummary {
	out := make([]entity.ToolSummary, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, entity.ToolSummary{Name: name})
	}
	return out
}

func (r *schemaRegistry) LoadSchemas(names []string) ([]entity.ToolDefinition, error) {
	r.requested = append([]string(nil), names...)
	defs := make([]entity.ToolDefinition, 0, len(names))
	for _, n := range names {
		if _, ok := r.tools[n]; ok {
			defs = append(defs, entity.ToolDefinition{ToolSummary: entity.ToolSummary{Name: n}, Schema: json.RawMessage(`{}`)})
		}
	}
	return defs, nil
}

func newSchemaRegistry(names ...string) *schemaRegistry {
	r := &schemaRegistry{stubRegistry: stubRegistry{tools: map[string]output.Tool{}}}
	for _, n := range names {
		r.tools[n] = &stubTool{name: n, result: `{}`}
	}
	return r
}

// A photo with a keyword-less caption ("Сделай секси") arriving after a
// document conversation: the sticky context offers only file tools, and the
// model then claims it cannot edit pictures. The attachment itself is the
// intent, so generate_image must be on the table whenever an image is sent.
func TestPipelineOffersImageToolWhenMessageHasImage(t *testing.T) {
	registry := newSchemaRegistry("generate_image", "drive_files", "read_pdf", "search_web")
	p := chat.NewPipeline(chat.NewRuleClassifier(), &stubLLM{}, nil, registry, nil, "", chat.ChatConfig{MaxTokens: 100})

	messages := []output.LLMMessage{
		{Role: "user", Content: "проанализируй документ из выписки"},
		{Role: "assistant", Content: "готово"},
		{Role: "user", Content: "Сделай секси", Images: []output.ImageContent{{Base64: "aGk=", MimeType: "image/jpeg"}}},
	}
	if _, err := p.Process(context.Background(), messages, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !contains(registry.requested, "generate_image") {
		t.Fatalf("generate_image not offered; requested schemas: %v", registry.requested)
	}
	if !contains(registry.requested, "drive_files") {
		t.Errorf("sticky context tools should be kept: %v", registry.requested)
	}
}

// Without any classifier hit the pipeline already loads every tool; adding the
// image tool must not narrow that down to a single schema.
func TestPipelineKeepsAllToolsForImageWithoutContext(t *testing.T) {
	registry := newSchemaRegistry("generate_image", "search_web")
	p := chat.NewPipeline(chat.NewRuleClassifier(), &stubLLM{}, nil, registry, nil, "", chat.ChatConfig{MaxTokens: 100})

	messages := []output.LLMMessage{
		{Role: "user", Content: "Сделай секси", Images: []output.ImageContent{{Base64: "aGk=", MimeType: "image/jpeg"}}},
	}
	if _, err := p.Process(context.Background(), messages, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(registry.requested, "generate_image") || !contains(registry.requested, "search_web") {
		t.Errorf("expected every tool, got %v", registry.requested)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
