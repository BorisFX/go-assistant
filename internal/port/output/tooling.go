package output

import (
	"context"
	"encoding/json"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type Tool interface {
	Name() string
	Description() string
	Category() string
	Schema() json.RawMessage
	Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error)
}

type ToolRegistry interface {
	Register(tool Tool) error
	ListTools() []entity.ToolSummary
	LoadSchema(name string) (*entity.ToolDefinition, error)
	LoadSchemas(names []string) ([]entity.ToolDefinition, error)
	GetTool(name string) (Tool, error)
}
