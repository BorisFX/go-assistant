package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// GmailClient is the slice of the Gmail adapter this tool needs.
type GmailClient interface {
	Search(ctx context.Context, query string, limit int) ([]gworkspace.Message, error)
	Get(ctx context.Context, id string) (gworkspace.Message, error)
	DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error)
	CreateDraft(ctx context.Context, out gworkspace.Outgoing) (gworkspace.DraftInfo, error)
	ListDrafts(ctx context.Context, limit int) ([]gworkspace.DraftInfo, error)
	User() string
}

// Gmail lets the assistant read the work mailbox and prepare outgoing letters.
// Sending is deliberately absent from the tool: a letter to a counterparty
// cannot be recalled, so the draft goes to Telegram and a human presses send.
type Gmail struct {
	client   GmailClient
	filesDir string
	onDraft  func(gworkspace.DraftInfo)
}

func NewGmail(client GmailClient, filesDir string) *Gmail {
	return &Gmail{client: client, filesDir: filesDir}
}

// SetOnDraft wires the confirmation card. Called after the bot is built, since
// the tool registry is assembled before it.
func (g *Gmail) SetOnDraft(fn func(gworkspace.DraftInfo)) { g.onDraft = fn }

func (g *Gmail) Name() string { return "gmail" }

func (g *Gmail) Description() string {
	return "Work mailbox (" + g.client.User() + "): search messages, read one with its body and attachments, download an attachment, prepare an outgoing letter as a draft. Drafts are never sent by you — the user confirms and sends them"
}

func (g *Gmail) Category() string { return "files" }

func (g *Gmail) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["search", "read", "download", "draft", "drafts"],
				"description": "Operation to perform. draft prepares a letter and asks the user to confirm it in Telegram; there is no way to send directly"
			},
			"query": {"type": "string", "description": "Gmail search query, e.g. from:example.com has:attachment newer_than:7d"},
			"message_id": {"type": "string", "description": "Message id, for read and download"},
			"attachment_id": {"type": "string", "description": "Attachment id from read, for download"},
			"name": {"type": "string", "description": "File name to save the attachment under, for download"},
			"limit": {"type": "integer", "description": "Maximum messages to return for search, default 10"},
			"to": {"type": "array", "items": {"type": "string"}, "description": "Recipients, for draft. One letter per contractor — never put competing contractors in one letter"},
			"cc": {"type": "array", "items": {"type": "string"}, "description": "Copy recipients, for draft"},
			"subject": {"type": "string", "description": "Subject, for draft"},
			"body": {"type": "string", "description": "Plain text body, for draft"},
			"thread_id": {"type": "string", "description": "Thread to reply into, for draft"},
			"in_reply_to": {"type": "string", "description": "Message-Id header of the letter being answered, for draft"},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "Paths of local files to attach, as returned by download actions"}
		},
		"required": ["action"]
	}`)
}

type gmailParams struct {
	Action       string   `json:"action"`
	Query        string   `json:"query"`
	MessageID    string   `json:"message_id"`
	AttachmentID string   `json:"attachment_id"`
	Name         string   `json:"name"`
	Limit        int      `json:"limit"`
	To           []string `json:"to"`
	Cc           []string `json:"cc"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	ThreadID     string   `json:"thread_id"`
	InReplyTo    string   `json:"in_reply_to"`
	Attachments  []string `json:"attachments"`
}

func (g *Gmail) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p gmailParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	switch p.Action {
	case "search":
		return g.search(ctx, p.Query, p.Limit)
	case "read":
		return g.read(ctx, p.MessageID)
	case "download":
		return g.download(ctx, p.MessageID, p.AttachmentID, p.Name)
	case "draft":
		return g.draft(ctx, p)
	case "drafts":
		return g.drafts(ctx, p.Limit)
	default:
		return nil, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (g *Gmail) search(ctx context.Context, query string, limit int) (json.RawMessage, error) {
	if query == "" {
		return nil, fmt.Errorf("gmail search: query is required")
	}
	if limit <= 0 {
		limit = 10
	}
	msgs, err := g.client.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"query": query, "messages": msgs})
}

// maxBodyChars keeps a forwarded thread from eating the context window; the full
// text stays one read away in Gmail itself.
const maxBodyChars = 8000

func (g *Gmail) read(ctx context.Context, id string) (json.RawMessage, error) {
	msg, err := g.client.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len([]rune(msg.Body)) > maxBodyChars {
		msg.Body = string([]rune(msg.Body)[:maxBodyChars]) + "\n…(текст письма обрезан)"
	}
	return json.Marshal(msg)
}

func (g *Gmail) download(ctx context.Context, messageID, attachmentID, name string) (json.RawMessage, error) {
	data, err := g.client.DownloadAttachment(ctx, messageID, attachmentID)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = attachmentID
	}
	dir := filepath.Join(g.filesDir, "gmail", sanitizeFileName(messageID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeFileName(name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return json.Marshal(map[string]any{"path": path, "size": len(data)})
}

// sanitizeFileName keeps an attachment name from escaping the files directory.
func sanitizeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == ".." || name == "/" {
		return "attachment"
	}
	return name
}

// maxAttachmentBytes is Gmail's ceiling for one message, minus room for base64
// inflation and headers.
const maxAttachmentBytes = 24 * 1024 * 1024

// draft prepares a letter and hands it to the user for confirmation. It never
// sends: the only path outward runs through a human pressing a button.
func (g *Gmail) draft(ctx context.Context, p gmailParams) (json.RawMessage, error) {
	out := gworkspace.Outgoing{
		To:        p.To,
		Cc:        p.Cc,
		Subject:   p.Subject,
		Body:      p.Body,
		ThreadID:  p.ThreadID,
		InReplyTo: p.InReplyTo,
	}
	for _, path := range p.Attachments {
		att, err := readAttachment(g.filesDir, path)
		if err != nil {
			return nil, err
		}
		out.Attachments = append(out.Attachments, att)
	}

	info, err := g.client.CreateDraft(ctx, out)
	if err != nil {
		return nil, err
	}
	if g.onDraft != nil {
		g.onDraft(info)
	}
	return json.Marshal(map[string]any{
		"draft_id": info.ID,
		"to":       info.To,
		"subject":  info.Subject,
		"status":   "черновик создан и НЕ отправлен; подтверждение ушло пользователю в Telegram",
	})
}

func (g *Gmail) drafts(ctx context.Context, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = 10
	}
	list, err := g.client.ListDrafts(ctx, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"drafts": list})
}

// readAttachment loads a file the model asked to attach. The path must stay
// inside the files directory: attachment paths come from model output, and
// "/etc/passwd" must not become a letter to a stranger.
func readAttachment(filesDir, path string) (gworkspace.OutgoingAttachment, error) {
	root, err := filepath.Abs(filesDir)
	if err != nil {
		return gworkspace.OutgoingAttachment{}, fmt.Errorf("files dir: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: разрешены только файлы из рабочей папки бота", path)
	}

	info, err := os.Stat(abs)
	if err != nil {
		// Модель держит в голове имя файла, а каталог угадывает: ТЗ, лежащее в
		// files/pdf/, она просит из files/. Ищем по имени, вместо того чтобы
		// заваливать рассылку из-за одной папки.
		found, ferr := findByName(root, filepath.Base(abs))
		if ferr != nil {
			return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: %w", path, ferr)
		}
		abs = found
		if info, err = os.Stat(abs); err != nil {
			return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: %w", path, err)
		}
		slog.Info("вложение найдено по имени", "просили", path, "взяли", abs)
	}
	if info.Size() > maxAttachmentBytes {
		return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: %d байт — больше лимита письма", path, info.Size())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return gworkspace.OutgoingAttachment{}, fmt.Errorf("вложение %s: %w", path, err)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(abs))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return gworkspace.OutgoingAttachment{
		Name:     filepath.Base(abs),
		MimeType: mimeType,
		Content:  data,
	}, nil
}

// findByName looks up a file by its name anywhere under the bot's directory and
// reports what is actually there when nothing matches.
func findByName(root, name string) (string, error) {
	var match string
	var candidates []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // нечитаемая ветка дерева не должна рушить поиск
		}
		if strings.EqualFold(d.Name(), name) {
			match = path
			return fs.SkipAll
		}
		if len(candidates) < 10 && isDocument(d.Name()) {
			candidates = append(candidates, d.Name())
		}
		return nil
	})
	if err == nil && match != "" {
		return match, nil
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("файла с таким именем нет в рабочей папке бота")
	}
	return "", fmt.Errorf("файла с таким именем нет; в рабочей папке есть: %s", strings.Join(candidates, ", "))
}

func isDocument(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".dwg", ".dxf":
		return true
	default:
		return false
	}
}
