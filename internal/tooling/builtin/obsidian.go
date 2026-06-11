package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

const maxReadBytes = 100_000

// resolveInVault joins rel onto vaultDir and rejects escapes.
func (o *Obsidian) resolveInVault(rel string) (string, error) {
	abs := filepath.Join(o.vaultDir, filepath.Clean("/"+rel))
	root, err := filepath.Abs(o.vaultDir)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the vault", rel)
	}
	return full, nil
}

type searchHit struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

func (o *Obsidian) search(query string) (json.RawMessage, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("search requires a non-empty query")
	}
	q := strings.ToLower(query)
	var hits []searchHit
	err := filepath.WalkDir(o.vaultDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(o.vaultDir, path)
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(body)
		nameMatch := strings.Contains(strings.ToLower(rel), q)
		idx := strings.Index(strings.ToLower(text), q)
		if !nameMatch && idx < 0 {
			return nil
		}
		snippet := ""
		if idx >= 0 {
			start := idx - 40
			if start < 0 {
				start = 0
			}
			end := idx + 80
			if end > len(text) {
				end = len(text)
			}
			snippet = strings.ReplaceAll(text[start:end], "\n", " ")
		}
		hits = append(hits, searchHit{Path: rel, Snippet: snippet})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search vault: %w", err)
	}
	return json.Marshal(map[string]any{"query": query, "hits": hits})
}

func (o *Obsidian) read(rel string) (json.RawMessage, error) {
	if strings.TrimSpace(rel) == "" {
		return nil, fmt.Errorf("read requires a path")
	}
	full, err := o.resolveInVault(rel)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read note: %w", err)
	}
	truncated := false
	if len(body) > maxReadBytes {
		body = body[:maxReadBytes]
		truncated = true
	}
	return json.Marshal(map[string]any{"path": rel, "content": string(body), "truncated": truncated})
}

func (o *Obsidian) ingest(ctx context.Context, content string) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented")
}
