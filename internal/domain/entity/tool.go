package entity

import "encoding/json"

type ToolSummary struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type ToolDefinition struct {
	ToolSummary
	Schema json.RawMessage `json:"input_schema"`
}
