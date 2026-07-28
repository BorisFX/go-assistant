package tooling

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Registry struct {
	mu    sync.RWMutex
	tools map[string]output.Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]output.Tool),
	}
}

func (r *Registry) Register(tool output.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[tool.Name()]; exists {
		return fmt.Errorf("tool %q already registered", tool.Name())
	}

	r.tools[tool.Name()] = tool
	return nil
}

func (r *Registry) ListTools() []entity.ToolSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	summaries := make([]entity.ToolSummary, 0, len(r.tools))
	for _, tool := range r.tools {
		summaries = append(summaries, entity.ToolSummary{
			Name:        tool.Name(),
			Category:    tool.Category(),
			Description: tool.Description(),
		})
	}
	return summaries
}

func (r *Registry) LoadSchema(name string) (*entity.ToolDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	return &entity.ToolDefinition{
		ToolSummary: entity.ToolSummary{
			Name:        tool.Name(),
			Category:    tool.Category(),
			Description: tool.Description(),
		},
		Schema: tool.Schema(),
	}, nil
}

// LoadSchemas returns schemas for the named tools, skipping any that are not
// registered. Classifier rules are shared across instances while the tool set is
// per-instance, so a missing tool is a normal configuration difference. Failing
// the whole batch would leave the caller with no tools at all — which is how a
// single unavailable name used to silently disarm the model.
func (r *Registry) LoadSchemas(names []string) ([]entity.ToolDefinition, error) {
	defs := make([]entity.ToolDefinition, 0, len(names))
	for _, name := range names {
		def, err := r.LoadSchema(name)
		if err != nil {
			slog.Debug("tool not registered on this instance, skipping", "tool", name)
			continue
		}
		defs = append(defs, *def)
	}
	if len(defs) == 0 && len(names) > 0 {
		return nil, fmt.Errorf("none of the requested tools are registered: %v", names)
	}
	return defs, nil
}

func (r *Registry) GetTool(name string) (output.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return tool, nil
}
