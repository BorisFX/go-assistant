package projects

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// Folder names the courier writes into. Correspondence is a stage folder of the
// route template; letters that resolve to no project wait in the unsorted one.
const (
	CorrespondenceFolder = "99_Переписка"
	UnsortedFolder       = "_Разобрать"
)

// inlineImageMaxBytes separates a real photo of a plan from the logo in an email
// signature. Signature images are a few kilobytes; a photographed drawing is
// never that small.
const inlineImageMaxBytes = 50 * 1024

// maxSubjectRunes keeps a folder name readable — Drive accepts long names, the
// person reading the list in Telegram does not.
const maxSubjectRunes = 60

// mailClient is the slice of the Gmail adapter the courier needs.
type mailClient interface {
	ListIDs(ctx context.Context, query string, limit int) ([]string, error)
	Get(ctx context.Context, id string) (gworkspace.Message, error)
	DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error)
	EnsureLabel(ctx context.Context, name string) (string, error)
	AddLabel(ctx context.Context, messageID, labelID string) error
}

// mailDriveClient is the write side of Drive; the read-only driveClient in
// service.go stays as it is so its fakes need no changes.
type mailDriveClient interface {
	EnsurePath(ctx context.Context, path string) (string, error)
	List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error)
	Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error)
}

type CourierConfig struct {
	Query       string
	Label       string
	MaxMessages int
	Interval    time.Duration
}

// Courier carries mail into Drive and stops there. It deliberately does not
// start a review: the expensive coordinator runs when the user asks for it with
// "разбери папку", not on every letter that arrives.
type Courier struct {
	mail     mailClient
	drive    mailDriveClient
	registry *Service
	cfg      CourierConfig
	notify   func(string)
}

func NewCourier(mail mailClient, drive mailDriveClient, registry *Service, cfg CourierConfig, notify func(string)) *Courier {
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 20
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Minute
	}
	return &Courier{mail: mail, drive: drive, registry: registry, cfg: cfg, notify: notify}
}

// SetNotify wires the Telegram sink after the bot is built. The courier itself
// is constructed earlier, together with the rest of the Google stack.
func (c *Courier) SetNotify(fn func(string)) { c.notify = fn }

// Delivery is what happened to one message.
type Delivery struct {
	MessageID  string
	Subject    string
	From       string
	Project    string
	Folder     string
	Saved      []string
	Candidates []string
	Err        error
}

// Run polls the mailbox until the context is cancelled.
func (c *Courier) Run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	// One pass right away: after a restart the mail that arrived while the bot
	// was down should not wait out a full interval.
	c.pass(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pass(ctx)
		}
	}
}

func (c *Courier) pass(ctx context.Context) {
	deliveries, err := c.Poll(ctx)
	if err != nil {
		slog.Error("mail courier: poll failed", "error", err)
		return
	}
	if text := FormatDigest(deliveries); text != "" && c.notify != nil {
		c.notify(text)
	}
}

// Poll processes one batch of unprocessed mail. A message that fails is left
// unlabelled and reported, so the next pass retries it — the same partial-failure
// policy the review orchestrator uses.
func (c *Courier) Poll(ctx context.Context) ([]Delivery, error) {
	labelID, err := c.mail.EnsureLabel(ctx, c.cfg.Label)
	if err != nil {
		// Without the label there is no way to mark mail as handled, and the
		// next pass would deliver the same attachments again.
		return nil, fmt.Errorf("courier: %w", err)
	}
	ids, err := c.mail.ListIDs(ctx, c.cfg.Query, c.cfg.MaxMessages)
	if err != nil {
		return nil, fmt.Errorf("courier: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	known, err := c.registry.Projects(ctx)
	if err != nil {
		// Delivering everything to the unsorted folder just because the registry
		// blinked would scatter documents the next pass could have filed.
		return nil, fmt.Errorf("courier: read registry: %w", err)
	}

	deliveries := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		d := c.deliver(ctx, id, known)
		if d.Err == nil {
			if err := c.mail.AddLabel(ctx, id, labelID); err != nil {
				d.Err = err
			}
		}
		if d.Err != nil {
			slog.Warn("mail courier: message failed", "id", id, "subject", d.Subject, "error", d.Err)
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, nil
}

func (c *Courier) deliver(ctx context.Context, messageID string, known []Project) Delivery {
	msg, err := c.mail.Get(ctx, messageID)
	if err != nil {
		return Delivery{MessageID: messageID, Err: err}
	}
	d := Delivery{MessageID: msg.ID, Subject: msg.Subject, From: msg.From}

	attachments := keepDocuments(msg.Attachments)
	if len(attachments) == 0 {
		return d // nothing to carry; the letter itself stays in Gmail
	}

	project, candidates := MatchProject(msg.Subject, msg.Body, msg.From, known)
	d.Project, d.Candidates = project.Name, candidates
	d.Folder = deliveryFolder(project.Name, msg.Subject, msg.Date)

	folderID, err := c.drive.EnsurePath(ctx, d.Folder)
	if err != nil {
		d.Err = err
		return d
	}
	existing, err := c.drive.List(ctx, folderID)
	if err != nil {
		d.Err = err
		return d
	}
	present := make(map[string]int64, len(existing))
	for _, f := range existing {
		present[f.Name] = f.Size
	}

	for _, att := range attachments {
		name := att.Name
		if size, taken := present[name]; taken {
			if size == att.Size {
				// Re-delivery after a crash between upload and labelling: the
				// file is already there, so this is a success, not a copy.
				continue
			}
			// Same name, different file — two contractors both send "КП.pdf"
			// into one folder, and neither may overwrite the other.
			name = uniqueName(name, present)
		}
		data, err := c.mail.DownloadAttachment(ctx, msg.ID, att.ID)
		if err != nil {
			d.Err = fmt.Errorf("скачать %s: %w", att.Name, err)
			return d
		}
		if _, err := c.drive.Upload(ctx, folderID, name, att.MimeType, data); err != nil {
			d.Err = fmt.Errorf("загрузить %s: %w", name, err)
			return d
		}
		present[name] = att.Size
		d.Saved = append(d.Saved, name)
	}
	return d
}

func uniqueName(name string, taken map[string]int64) string {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

// keepDocuments drops what is not worth filing: signature logos and empty parts.
func keepDocuments(attachments []gworkspace.Attachment) []gworkspace.Attachment {
	out := make([]gworkspace.Attachment, 0, len(attachments))
	for _, a := range attachments {
		if a.Name == "" {
			continue
		}
		if strings.HasPrefix(a.MimeType, "image/") && a.Size > 0 && a.Size < inlineImageMaxBytes {
			continue
		}
		out = append(out, a)
	}
	return out
}

// deliveryFolder is "<Проект>/99_Переписка/ГГГГ-ММ-ДД_тема", or the unsorted
// folder when no project was recognised. One batch per letter: a later pass
// never mixes new documents into an already reviewed folder.
func deliveryFolder(project, subject, date string) string {
	day := time.Now().Format("2006-01-02")
	if t, err := time.Parse(time.RFC3339, date); err == nil {
		day = t.Format("2006-01-02")
	}
	leaf := day + "_" + sanitizeSegment(subject)
	if project == "" {
		return UnsortedFolder + "/" + leaf
	}
	return project + "/" + CorrespondenceFolder + "/" + leaf
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "без темы"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || unicode.IsControl(r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if runes := []rune(out); len(runes) > maxSubjectRunes {
		out = strings.TrimSpace(string(runes[:maxSubjectRunes]))
	}
	return out
}

// FormatDigest renders the Telegram message. It names the folder in the exact
// form the review intent expects, so the next step is a copy-paste away.
func FormatDigest(deliveries []Delivery) string {
	var lines, reasons []string
	var failed, empty int

	for _, d := range deliveries {
		switch {
		case d.Err != nil:
			failed++
			if len(reasons) < 2 {
				reasons = append(reasons, d.Err.Error())
			}
		case len(d.Saved) == 0:
			empty++
		default:
			where := d.Project
			if where == "" {
				where = "проект не распознан"
				if len(d.Candidates) > 1 {
					where = "подходят " + strings.Join(d.Candidates, ", ")
				}
			}
			lines = append(lines, fmt.Sprintf("• %s — %d файлов, %s\n  разбери папку %s",
				where, len(d.Saved), d.Subject, d.Folder))
		}
	}

	if len(lines) == 0 && failed == 0 {
		return "" // nothing arrived worth reporting; stay quiet
	}

	var b strings.Builder
	b.WriteString("📬 Почта\n")
	b.WriteString(strings.Join(lines, "\n"))
	if empty > 0 {
		fmt.Fprintf(&b, "\nБез вложений: %d", empty)
	}
	if failed > 0 {
		fmt.Fprintf(&b, "\nНе разобрано: %d — повторю в следующий проход", failed)
		for _, r := range reasons {
			fmt.Fprintf(&b, "\n  %s", r)
		}
	}
	return strings.TrimSpace(b.String())
}
