package google_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

func TestOutgoingValidate(t *testing.T) {
	base := gworkspace.Outgoing{
		To: []string{"p@example.com"}, Subject: "КП", Body: "текст",
	}

	cases := []struct {
		name string
		mut  func(*gworkspace.Outgoing)
		ok   bool
	}{
		{"полное письмо", func(*gworkspace.Outgoing) {}, true},
		{"с именем адресата", func(o *gworkspace.Outgoing) { o.To = []string{"Иван <i@example.com>"} }, true},
		{"без получателя", func(o *gworkspace.Outgoing) { o.To = nil }, false},
		{"кривой адрес", func(o *gworkspace.Outgoing) { o.To = []string{"не адрес"} }, false},
		{"кривая копия", func(o *gworkspace.Outgoing) { o.Cc = []string{"@@"} }, false},
		{"пустая тема", func(o *gworkspace.Outgoing) { o.Subject = "  " }, false},
		{"пустой текст", func(o *gworkspace.Outgoing) { o.Body = "" }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.mut(&o)
			err := o.Validate()
			if tc.ok != (err == nil) {
				t.Errorf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// Cyrillic in a subject must survive the trip: unencoded it reaches the
// counterparty as mojibake, and a tender letter is read by a stranger.
func TestBuildMIMEEncodesSubject(t *testing.T) {
	raw := mustDraft(t, gworkspace.Outgoing{
		To:      []string{"p@example.com"},
		Cc:      []string{"buh@example.com"},
		Subject: "Запрос КП: геодезия, Ленина 42",
		Body:    "Добрый день!",
	})

	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if subject != "Запрос КП: геодезия, Ленина 42" {
		t.Errorf("subject: %q", subject)
	}
	if msg.Header.Get("Cc") != "buh@example.com" {
		t.Errorf("cc: %q", msg.Header.Get("Cc"))
	}
	if msg.Header.Get("From") != "" {
		t.Error("From must be left to Gmail")
	}
	body, _ := io.ReadAll(msg.Body)
	if string(body) != "Добрый день!" {
		t.Errorf("body: %q", body)
	}
}

func TestBuildMIMEAttachesFiles(t *testing.T) {
	raw := mustDraft(t, gworkspace.Outgoing{
		To:      []string{"p@example.com"},
		Subject: "ТЗ на геодезию",
		Body:    "ТЗ во вложении",
		Attachments: []gworkspace.OutgoingAttachment{
			{Name: "ТЗ.pdf", MimeType: "application/pdf", Content: []byte("%PDF-1.4 плюс кириллица")},
		},
	})

	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type: %q %v", mediaType, err)
	}

	r := multipart.NewReader(msg.Body, params["boundary"])
	text, err := r.NextPart()
	if err != nil {
		t.Fatalf("text part: %v", err)
	}
	if data, _ := io.ReadAll(text); string(data) != "ТЗ во вложении" {
		t.Errorf("text part: %q", data)
	}

	file, err := r.NextPart()
	if err != nil {
		t.Fatalf("attachment part: %v", err)
	}
	name, err := new(mime.WordDecoder).DecodeHeader(file.FileName())
	if err != nil {
		t.Fatalf("decode file name: %v", err)
	}
	if name != "ТЗ.pdf" {
		t.Errorf("attachment name: %q", name)
	}
	encoded, _ := io.ReadAll(file)
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(encoded), "\r\n", ""))
	if err != nil {
		t.Fatalf("attachment not base64: %v", err)
	}
	if string(decoded) != "%PDF-1.4 плюс кириллица" {
		t.Errorf("attachment content: %q", decoded)
	}
}

// A reply must land in the same thread, or the answer to a tender arrives as a
// new conversation and the binding by threadId is lost.
func TestBuildMIMEThreadsReplies(t *testing.T) {
	raw := mustDraft(t, gworkspace.Outgoing{
		To:        []string{"p@example.com"},
		Subject:   "Re: КП",
		Body:      "принято",
		InReplyTo: "<abc@mail.example.com>",
	})

	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	if msg.Header.Get("In-Reply-To") != "<abc@mail.example.com>" {
		t.Errorf("in-reply-to: %q", msg.Header.Get("In-Reply-To"))
	}
	if msg.Header.Get("References") != "<abc@mail.example.com>" {
		t.Errorf("references: %q", msg.Header.Get("References"))
	}
}

// mustDraft builds a draft through the adapter and returns the raw RFC 2822
// bytes the API received, which is the only way to inspect what was assembled.
func mustDraft(t *testing.T, out gworkspace.Outgoing) string {
	t.Helper()
	stub := newGmailStub(t, map[string]string{"/drafts": `{"id":"r1","message":{"id":"m1","threadId":"t1"}}`})
	if _, err := newTestGmail(t, stub).CreateDraft(t.Context(), out); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	var body struct {
		Message struct {
			Raw string `json:"raw"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(stub.bodies[0]), &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(body.Message.Raw)
	if err != nil {
		t.Fatalf("raw not base64url: %v", err)
	}
	return string(raw)
}
