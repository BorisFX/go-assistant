package builtin

import (
	"context"
	"encoding/json"
	"fmt"
)

type Obsidian struct {
	vaultDir string
	executor CodeExecutor
}

func NewObsidian(vaultDir string, executor CodeExecutor) *Obsidian {
	return &Obsidian{vaultDir: vaultDir, executor: executor}
}

func (o *Obsidian) Name() string     { return "obsidian" }
func (o *Obsidian) Category() string { return "knowledge" }
func (o *Obsidian) Description() string {
	return "Access the personal Obsidian vault: search and read notes, or run workflows (ingest research, weekly_review, cross_pollinate)"
}

func (o *Obsidian) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["search", "read", "ingest", "weekly_review", "cross_pollinate"],
				"description": "search: find notes by text; read: read a note by relative path; ingest: save 'content' to the inbox and summarize it into the vault; weekly_review: generate the weekly report; cross_pollinate: find non-obvious links for the note in 'note'"
			},
			"query": {"type": "string", "description": "search: text to look for in filenames and note content"},
			"path": {"type": "string", "description": "read: note path relative to the vault root"},
			"content": {"type": "string", "description": "ingest: raw text (article/transcript) to file into the vault"},
			"note": {"type": "string", "description": "cross_pollinate: vault-relative path of the idea note"}
		},
		"required": ["action"]
	}`)
}

type obsidianParams struct {
	Action  string `json:"action"`
	Query   string `json:"query"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Note    string `json:"note"`
}

func (o *Obsidian) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p obsidianParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	switch p.Action {
	case "search":
		return o.search(p.Query)
	case "read":
		return o.read(p.Path)
	case "ingest":
		return o.ingest(ctx, p.Content)
	case "weekly_review":
		return o.delegate(ctx, "/weekly-review")
	case "cross_pollinate":
		return o.delegate(ctx, "/cross-pollinate "+p.Note)
	default:
		return nil, fmt.Errorf("unknown action %q", p.Action)
	}
}

func (o *Obsidian) delegate(ctx context.Context, command string) (json.RawMessage, error) {
	return o.executor.ExecuteJSON(ctx, command, o.vaultDir, nil)
}

func (o *Obsidian) search(query string) (json.RawMessage, error) { return nil, fmt.Errorf("not implemented") }
func (o *Obsidian) read(path string) (json.RawMessage, error)    { return nil, fmt.Errorf("not implemented") }
func (o *Obsidian) ingest(ctx context.Context, content string) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented")
}
