package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
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
	// Lexical check passed; now defend against symlinks inside the vault that
	// point outside it. Resolve both the target and root, then re-check the
	// prefix. A not-exist error is fine here — let read surface it normally.
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return full, nil
		}
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if resolved != rootResolved && !strings.HasPrefix(resolved, rootResolved+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the vault via symlink", rel)
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
			// Snap to rune boundaries so we never cut a multi-byte rune
			// (the vault is largely Cyrillic).
			for start > 0 && !utf8.RuneStart(text[start]) {
				start--
			}
			for end < len(text) && !utf8.RuneStart(text[end]) {
				end++
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
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("ingest requires content")
	}
	inbox := filepath.Join(o.vaultDir, "_agent", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return nil, fmt.Errorf("ensure inbox: %w", err)
	}
	slug := slugify(firstLine(content))
	name := slug + ".md"
	// Avoid silently overwriting an earlier ingest with the same first line:
	// append -2, -3, ... until the name is free.
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(inbox, name)); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s-%d.md", slug, i)
	}
	if err := os.WriteFile(filepath.Join(inbox, name), []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write inbox file: %w", err)
	}
	return o.delegate(ctx, "/ingest")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "inbox-item"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
