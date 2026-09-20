package google

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
)

// RfqHeader marks a letter as part of a bot-made mailing. Drafts written by a
// human never carry it, so cleanup can never touch them.
const RfqHeader = "X-Assistant-Rfq"

// OutgoingAttachment is a file to attach. The bytes are held in memory: Gmail
// caps a message at 35 MB, which is well inside what a single request carries.
type OutgoingAttachment struct {
	Name     string
	MimeType string
	Content  []byte
}

// Outgoing is a letter to be drafted. From is never set here — Gmail stamps the
// impersonated mailbox itself, and a forged From would be rejected anyway.
type Outgoing struct {
	// Tag помечает письма одной рассылки. Тема меняется от правки к правке, а
	// метка — нет: по ней потом находятся и убираются прежние черновики.
	Tag         string
	To          []string
	Cc          []string
	Subject     string
	Body        string
	ThreadID    string // reply inside an existing thread
	InReplyTo   string // Message-Id of the letter being answered
	Attachments []OutgoingAttachment
}

// Validate checks what Gmail would otherwise reject after the letter is already
// half-built, and what a model gets wrong most often: a missing or malformed
// address.
func (o Outgoing) Validate() error {
	if len(o.To) == 0 {
		return fmt.Errorf("письмо: не указан получатель")
	}
	for _, addr := range append(append([]string{}, o.To...), o.Cc...) {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("письмо: адрес %q не разобран: %w", addr, err)
		}
	}
	if strings.TrimSpace(o.Subject) == "" {
		return fmt.Errorf("письмо: пустая тема")
	}
	if strings.TrimSpace(o.Body) == "" {
		return fmt.Errorf("письмо: пустой текст")
	}
	return nil
}

// buildMIME renders RFC 2822 bytes. Subject and attachment names go through
// B-encoding, without which Cyrillic arrives at the counterparty as mojibake.
func buildMIME(o Outgoing) ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	header := func(name, value string) {
		fmt.Fprintf(&buf, "%s: %s\r\n", name, value)
	}
	header("To", strings.Join(o.To, ", "))
	if len(o.Cc) > 0 {
		header("Cc", strings.Join(o.Cc, ", "))
	}
	header("Subject", mime.BEncoding.Encode("UTF-8", o.Subject))
	if o.InReplyTo != "" {
		// Both headers: In-Reply-To threads the reply, References keeps the
		// chain intact for clients that build the tree from it.
		header("In-Reply-To", o.InReplyTo)
		header("References", o.InReplyTo)
	}
	if o.Tag != "" {
		header(RfqHeader, o.Tag)
	}
	header("MIME-Version", "1.0")

	if len(o.Attachments) == 0 {
		header("Content-Type", `text/plain; charset="UTF-8"`)
		header("Content-Transfer-Encoding", "8bit")
		buf.WriteString("\r\n")
		buf.WriteString(o.Body)
		return buf.Bytes(), nil
	}

	w := multipart.NewWriter(&buf)
	header("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", w.Boundary()))
	buf.WriteString("\r\n")

	text, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {`text/plain; charset="UTF-8"`},
		"Content-Transfer-Encoding": {"8bit"},
	})
	if err != nil {
		return nil, fmt.Errorf("письмо: часть с текстом: %w", err)
	}
	if _, err := text.Write([]byte(o.Body)); err != nil {
		return nil, fmt.Errorf("письмо: текст: %w", err)
	}

	for _, att := range o.Attachments {
		mimeType := att.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		name := mime.BEncoding.Encode("UTF-8", att.Name)
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {fmt.Sprintf("%s; name=%q", mimeType, name)},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", name)},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, fmt.Errorf("письмо: вложение %s: %w", att.Name, err)
		}
		if err := writeBase64Lines(part, att.Content); err != nil {
			return nil, fmt.Errorf("письмо: вложение %s: %w", att.Name, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("письмо: закрыть multipart: %w", err)
	}
	return buf.Bytes(), nil
}

// writeBase64Lines wraps at 76 characters. Some mail servers still reject a
// single multi-megabyte line, and RFC 2045 asks for the wrap anyway.
func writeBase64Lines(w io.Writer, data []byte) error {
	const lineLen = 76
	encoded := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(encoded); i += lineLen {
		end := min(i+lineLen, len(encoded))
		if _, err := io.WriteString(w, encoded[i:end]+"\r\n"); err != nil {
			return err
		}
	}
	return nil
}
