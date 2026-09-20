package google

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// maxSearchResults caps a single search. Gmail pages results, and an unbounded
// walk over a busy mailbox would burn quota inside one tool call.
const maxSearchResults = 50

// Attachment is one file carried by a message. The bytes are fetched
// separately: a scan of a tech plan is tens of megabytes and has no business
// inside a search result.
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size,omitempty"`
}

// Message is the trimmed view of a Gmail message. Body and Attachments are
// filled by Get; Search returns headers only.
type Message struct {
	ID          string       `json:"id"`
	ThreadID    string       `json:"thread_id"`
	From        string       `json:"from"`
	To          string       `json:"to"`
	Subject     string       `json:"subject"`
	Date        string       `json:"date,omitempty"`
	Snippet     string       `json:"snippet,omitempty"`
	Body        string       `json:"body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Gmail wraps the Gmail v1 service pinned to a single mailbox. Unlike Drive and
// Sheets, Gmail refuses to act as a service account, so every call runs on
// behalf of the impersonated user — see Credentials.ClientOptions.
type Gmail struct {
	svc  *gmail.Service
	user string
}

func NewGmail(ctx context.Context, user string, opts ...option.ClientOption) (*Gmail, error) {
	if user == "" {
		return nil, fmt.Errorf("gmail: mailbox address is required (google.impersonate)")
	}
	svc, err := gmail.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gmail service: %w", err)
	}
	return &Gmail{svc: svc, user: user}, nil
}

// User is the mailbox this adapter acts as.
func (g *Gmail) User() string { return g.user }

// Search runs a Gmail query and returns message headers, newest first. The
// query syntax is Gmail's own, so "from:", "has:attachment" and "newer_than:"
// all work as they do in the web interface.
func (g *Gmail) Search(ctx context.Context, query string, limit int) ([]Message, error) {
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	list, err := g.svc.Users.Messages.List(g.user).
		Q(query).MaxResults(int64(limit)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail search %q: %w", query, err)
	}

	out := make([]Message, 0, len(list.Messages))
	for _, m := range list.Messages {
		full, err := g.svc.Users.Messages.Get(g.user, m.Id).
			Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Date").
			Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail get %s: %w", m.Id, err)
		}
		out = append(out, toMessage(full))
	}
	return out, nil
}

// ListIDs returns only the message ids matching a query. The courier fetches
// each message in full anyway, so pulling headers here would double the calls.
func (g *Gmail) ListIDs(ctx context.Context, query string, limit int) ([]string, error) {
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	list, err := g.svc.Users.Messages.List(g.user).
		Q(query).MaxResults(int64(limit)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail search %q: %w", query, err)
	}
	ids := make([]string, 0, len(list.Messages))
	for _, m := range list.Messages {
		ids = append(ids, m.Id)
	}
	return ids, nil
}

// Get returns a message with its plain-text body and the list of attachments.
func (g *Gmail) Get(ctx context.Context, id string) (Message, error) {
	if id == "" {
		return Message{}, fmt.Errorf("gmail get: message id is required")
	}
	full, err := g.svc.Users.Messages.Get(g.user, id).Format("full").Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("gmail get %s: %w", id, err)
	}
	msg := toMessage(full)
	if full.Payload != nil {
		var body strings.Builder
		walkParts(full.Payload, &body, &msg.Attachments)
		msg.Body = strings.TrimSpace(body.String())
	}
	return msg, nil
}

// DownloadAttachment fetches one attachment's bytes.
func (g *Gmail) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	if messageID == "" || attachmentID == "" {
		return nil, fmt.Errorf("gmail attachment: message id and attachment id are required")
	}
	part, err := g.svc.Users.Messages.Attachments.
		Get(g.user, messageID, attachmentID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail attachment %s: %w", attachmentID, err)
	}
	data, err := base64.URLEncoding.DecodeString(part.Data)
	if err != nil {
		return nil, fmt.Errorf("gmail attachment %s: decode: %w", attachmentID, err)
	}
	return data, nil
}

// EnsureLabel returns the id of a label, creating it when missing. Labels are
// how a processed message is marked, so the first run on a fresh mailbox must
// not fail just because the label was never created by hand.
func (g *Gmail) EnsureLabel(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("gmail label: name is required")
	}
	list, err := g.svc.Users.Labels.List(g.user).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmail labels: %w", err)
	}
	for _, l := range list.Labels {
		if strings.EqualFold(l.Name, name) {
			return l.Id, nil
		}
	}
	created, err := g.svc.Users.Labels.Create(g.user, &gmail.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmail create label %q: %w", name, err)
	}
	return created.Id, nil
}

// AddLabel marks a message, which is what keeps the ingest query from picking
// it up again on the next pass.
func (g *Gmail) AddLabel(ctx context.Context, messageID, labelID string) error {
	if messageID == "" || labelID == "" {
		return fmt.Errorf("gmail add label: message id and label id are required")
	}
	_, err := g.svc.Users.Messages.Modify(g.user, messageID, &gmail.ModifyMessageRequest{
		AddLabelIds: []string{labelID},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gmail label %s on %s: %w", labelID, messageID, err)
	}
	return nil
}

// DraftInfo is a saved draft, as shown for confirmation before sending.
type DraftInfo struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id,omitempty"`
	To       string `json:"to,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	// Tag is the X-Assistant-Rfq value: which bot mailing this draft belongs to.
	Tag string `json:"tag,omitempty"`
}

// CreateDraft saves a letter without sending it. Everything the assistant writes
// outward stops here: a sent letter cannot be recalled, so a human confirms.
func (g *Gmail) CreateDraft(ctx context.Context, out Outgoing) (DraftInfo, error) {
	raw, err := buildMIME(out)
	if err != nil {
		return DraftInfo{}, err
	}
	draft, err := g.svc.Users.Drafts.Create(g.user, &gmail.Draft{
		Message: &gmail.Message{
			Raw:      base64.RawURLEncoding.EncodeToString(raw),
			ThreadId: out.ThreadID,
		},
	}).Context(ctx).Do()
	if err != nil {
		return DraftInfo{}, fmt.Errorf("gmail create draft: %w", err)
	}
	info := DraftInfo{ID: draft.Id, To: strings.Join(out.To, ", "), Subject: out.Subject}
	if draft.Message != nil {
		info.ThreadID = draft.Message.ThreadId
	}
	return info, nil
}

// SendDraft sends a saved draft. Called only from an explicit human confirmation.
func (g *Gmail) SendDraft(ctx context.Context, draftID string) (Message, error) {
	if draftID == "" {
		return Message{}, fmt.Errorf("gmail send: draft id is required")
	}
	sent, err := g.svc.Users.Drafts.Send(g.user, &gmail.Draft{Id: draftID}).Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("gmail send draft %s: %w", draftID, err)
	}
	return toMessage(sent), nil
}

// DeleteDraft discards a draft the user rejected.
func (g *Gmail) DeleteDraft(ctx context.Context, draftID string) error {
	if draftID == "" {
		return fmt.Errorf("gmail delete draft: draft id is required")
	}
	if err := g.svc.Users.Drafts.Delete(g.user, draftID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("gmail delete draft %s: %w", draftID, err)
	}
	return nil
}

// ListDrafts returns pending drafts with enough detail to recognise them. Used
// by the /drafts command, which is the way back to a confirmation whose message
// scrolled out of the chat.
func (g *Gmail) ListDrafts(ctx context.Context, limit int) ([]DraftInfo, error) {
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	list, err := g.svc.Users.Drafts.List(g.user).MaxResults(int64(limit)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail drafts: %w", err)
	}

	out := make([]DraftInfo, 0, len(list.Drafts))
	for _, d := range list.Drafts {
		info := DraftInfo{ID: d.Id}
		// Drafts.Get не принимает список заголовков: формат metadata и так
		// отдаёт их целиком, включая нашу метку.
		full, err := g.svc.Users.Drafts.Get(g.user, d.Id).Format("metadata").Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail draft %s: %w", d.Id, err)
		}
		if full.Message != nil {
			msg := toMessage(full.Message)
			info.ThreadID, info.To, info.Subject, info.Snippet = msg.ThreadID, msg.To, msg.Subject, msg.Snippet
			if full.Message.Payload != nil {
				for _, h := range full.Message.Payload.Headers {
					if strings.EqualFold(h.Name, RfqHeader) {
						info.Tag = h.Value
					}
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func toMessage(m *gmail.Message) Message {
	msg := Message{ID: m.Id, ThreadID: m.ThreadId, Snippet: html.UnescapeString(m.Snippet)}
	if m.Payload != nil {
		for _, h := range m.Payload.Headers {
			switch strings.ToLower(h.Name) {
			case "from":
				msg.From = h.Value
			case "to":
				msg.To = h.Value
			case "subject":
				msg.Subject = h.Value
			}
		}
	}
	if m.InternalDate > 0 {
		msg.Date = time.UnixMilli(m.InternalDate).UTC().Format(time.RFC3339)
	}
	return msg
}

// walkParts descends the MIME tree collecting the plain-text body and every
// attachment. A message from a law firm is routinely multipart/alternative
// nested inside multipart/mixed, so a flat scan of the top level misses both.
func walkParts(part *gmail.MessagePart, body *strings.Builder, attachments *[]Attachment) {
	if part == nil {
		return
	}
	if part.Body != nil && part.Body.AttachmentId != "" {
		name := part.Filename
		if name == "" {
			name = part.Body.AttachmentId
		}
		*attachments = append(*attachments, Attachment{
			ID:       part.Body.AttachmentId,
			Name:     name,
			MimeType: part.MimeType,
			Size:     part.Body.Size,
		})
	}

	if part.Filename == "" && part.Body != nil && part.Body.Data != "" {
		switch {
		case strings.HasPrefix(part.MimeType, "text/plain"):
			body.WriteString(decodePart(part.Body.Data))
			body.WriteString("\n")
		case strings.HasPrefix(part.MimeType, "text/html") && body.Len() == 0:
			// Only when no plain-text alternative showed up: stripped HTML is
			// worse input for both the reader and the project matcher.
			body.WriteString(stripHTML(decodePart(part.Body.Data)))
			body.WriteString("\n")
		}
	}

	for _, p := range part.Parts {
		walkParts(p, body, attachments)
	}
}

func decodePart(data string) string {
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Gmail omits padding on some parts; the raw-encoding variant accepts it.
		decoded, err = base64.RawURLEncoding.DecodeString(data)
		if err != nil {
			return ""
		}
	}
	return string(decoded)
}

var (
	htmlBlock = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRuns = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(s string) string {
	s = htmlBlock.ReplaceAllString(s, "")
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n").Replace(s)
	s = htmlTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(blankRuns.ReplaceAllString(s, "\n\n"))
}
