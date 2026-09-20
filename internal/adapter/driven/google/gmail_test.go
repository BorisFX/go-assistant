package google_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"google.golang.org/api/option"
)

// gmailStub answers by path prefix, which is enough to cover the handful of
// endpoints this adapter touches without pulling in a mock framework.
type gmailStub struct {
	*httptest.Server
	routes   map[string]string
	requests []string
	bodies   []string
}

func newGmailStub(t *testing.T, routes map[string]string) *gmailStub {
	t.Helper()
	s := &gmailStub{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.requests = append(s.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		s.bodies = append(s.bodies, string(body))

		for suffix, response := range s.routes {
			if strings.HasSuffix(r.URL.Path, suffix) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
				return
			}
		}
		http.Error(w, "no stub for "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestGmail(t *testing.T, stub *gmailStub) *gworkspace.Gmail {
	t.Helper()
	g, err := gworkspace.NewGmail(context.Background(), "info@samostrou.net",
		option.WithoutAuthentication(),
		option.WithEndpoint(stub.URL+"/"),
	)
	if err != nil {
		t.Fatalf("new gmail: %v", err)
	}
	return g
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func TestGmailRequiresMailbox(t *testing.T) {
	if _, err := gworkspace.NewGmail(context.Background(), "", option.WithoutAuthentication()); err == nil {
		t.Fatal("expected an error when the mailbox is empty")
	}
}

func TestGmailSearchReturnsHeaders(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/messages/m1": `{"id":"m1","threadId":"t1","internalDate":"1750000000000","snippet":"смета на &amp; подпись","payload":{"headers":[
			{"name":"From","value":"Подрядчик <p@example.com>"},
			{"name":"Subject","value":"КП по Ленина 42"}]}}`,
		"/messages": `{"messages":[{"id":"m1"}]}`,
	})

	msgs, err := newTestGmail(t, stub).Search(context.Background(), "in:inbox", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	got := msgs[0]
	if got.Subject != "КП по Ленина 42" || got.From != "Подрядчик <p@example.com>" {
		t.Errorf("headers: %+v", got)
	}
	if got.ThreadID != "t1" {
		t.Errorf("thread id: %q", got.ThreadID)
	}
	if !strings.HasPrefix(got.Date, "2025-") {
		t.Errorf("date not derived from internalDate: %q", got.Date)
	}
	if got.Snippet != "смета на & подпись" {
		t.Errorf("snippet not unescaped: %q", got.Snippet)
	}
}

func TestGmailSearchCapsLimit(t *testing.T) {
	stub := newGmailStub(t, map[string]string{"/messages": `{"messages":[]}`})

	if _, err := newTestGmail(t, stub).Search(context.Background(), "in:inbox", 5000); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(stub.requests[0], "maxResults=50") {
		t.Errorf("limit not capped: %s", stub.requests[0])
	}
}

// A real message from a law firm nests the text body two levels deep and hangs
// the scan off the outer part, so a flat scan of the top level finds neither.
func TestGmailGetWalksNestedParts(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/messages/m1": `{"id":"m1","threadId":"t1","payload":{"mimeType":"multipart/mixed",
			"headers":[{"name":"Subject","value":"Приостановка"}],
			"parts":[
				{"mimeType":"multipart/alternative","parts":[
					{"mimeType":"text/plain","body":{"data":"` + b64("Ленина 42, приостановка по ст.26") + `"}},
					{"mimeType":"text/html","body":{"data":"` + b64("<p>should be ignored</p>") + `"}}
				]},
				{"mimeType":"application/pdf","filename":"Уведомление.pdf","body":{"attachmentId":"a1","size":12345}}
			]}}`,
	})

	msg, err := newTestGmail(t, stub).Get(context.Background(), "m1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if msg.Body != "Ленина 42, приостановка по ст.26" {
		t.Errorf("body: %q", msg.Body)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.ID != "a1" || att.Name != "Уведомление.pdf" || att.Size != 12345 {
		t.Errorf("attachment: %+v", att)
	}
}

func TestGmailGetFallsBackToHTML(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/messages/m2": `{"id":"m2","payload":{"mimeType":"text/html","body":{"data":"` +
			b64("<style>p{}</style><p>Ленина&nbsp;42</p><br>готово") + `"}}}`,
	})

	msg, err := newTestGmail(t, stub).Get(context.Background(), "m2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(msg.Body, "Ленина") || strings.Contains(msg.Body, "<") {
		t.Errorf("html not stripped: %q", msg.Body)
	}
	if strings.Contains(msg.Body, "p{}") {
		t.Errorf("style block leaked into the body: %q", msg.Body)
	}
}

func TestGmailDownloadAttachment(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/attachments/a1": `{"attachmentId":"a1","size":5,"data":"` + b64("PDF-1") + `"}`,
	})

	data, err := newTestGmail(t, stub).DownloadAttachment(context.Background(), "m1", "a1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(data) != "PDF-1" {
		t.Errorf("content: %q", data)
	}
}

func TestGmailEnsureLabelReusesExisting(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/labels": `{"labels":[{"id":"Label_7","name":"обработано"}]}`,
	})

	id, err := newTestGmail(t, stub).EnsureLabel(context.Background(), "Обработано")
	if err != nil {
		t.Fatalf("ensure label: %v", err)
	}
	if id != "Label_7" {
		t.Errorf("label id: %q", id)
	}
	if len(stub.requests) != 1 {
		t.Errorf("expected no create call, got %v", stub.requests)
	}
}

func TestGmailEnsureLabelCreatesMissing(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"Label_9","name":"Обработано"}`))
			return
		}
		_, _ = w.Write([]byte(`{"labels":[{"id":"INBOX","name":"INBOX"}]}`))
	}))
	defer server.Close()

	g, err := gworkspace.NewGmail(context.Background(), "info@samostrou.net",
		option.WithoutAuthentication(), option.WithEndpoint(server.URL+"/"))
	if err != nil {
		t.Fatalf("new gmail: %v", err)
	}
	id, err := g.EnsureLabel(context.Background(), "Обработано")
	if err != nil {
		t.Fatalf("ensure label: %v", err)
	}
	if id != "Label_9" || calls != 2 {
		t.Errorf("id %q after %d calls", id, calls)
	}
}

func TestGmailAddLabel(t *testing.T) {
	stub := newGmailStub(t, map[string]string{"/modify": `{"id":"m1"}`})

	if err := newTestGmail(t, stub).AddLabel(context.Background(), "m1", "Label_7"); err != nil {
		t.Fatalf("add label: %v", err)
	}
	if !strings.Contains(stub.bodies[0], "Label_7") {
		t.Errorf("modify body: %s", stub.bodies[0])
	}
}

func TestGmailAddLabelValidates(t *testing.T) {
	stub := newGmailStub(t, map[string]string{"/modify": `{}`})
	if err := newTestGmail(t, stub).AddLabel(context.Background(), "", "Label_7"); err == nil {
		t.Error("expected an error when the message id is empty")
	}
}

func TestGmailSendDraft(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/drafts/send": `{"id":"m9","threadId":"t9","payload":{"headers":[{"name":"Subject","value":"Запрос КП"}]}}`,
	})

	msg, err := newTestGmail(t, stub).SendDraft(context.Background(), "r1")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if msg.ID != "m9" || msg.Subject != "Запрос КП" {
		t.Errorf("sent message: %+v", msg)
	}
	if !strings.Contains(stub.bodies[0], "r1") {
		t.Errorf("draft id not sent: %s", stub.bodies[0])
	}
}

func TestGmailSendDraftValidates(t *testing.T) {
	stub := newGmailStub(t, map[string]string{"/drafts/send": `{}`})
	if _, err := newTestGmail(t, stub).SendDraft(context.Background(), ""); err == nil {
		t.Error("expected an error without a draft id")
	}
}

func TestGmailDeleteDraft(t *testing.T) {
	stub := newGmailStub(t, map[string]string{"/drafts/r1": `{}`})

	if err := newTestGmail(t, stub).DeleteDraft(context.Background(), "r1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(stub.requests[0], "DELETE") {
		t.Errorf("expected a DELETE, got %s", stub.requests[0])
	}
}

func TestGmailListDrafts(t *testing.T) {
	stub := newGmailStub(t, map[string]string{
		"/drafts/r1": `{"id":"r1","message":{"id":"m1","threadId":"t1","snippet":"Добрый день","payload":{"headers":[
			{"name":"To","value":"p@example.com"},{"name":"Subject","value":"Запрос КП"}]}}}`,
		"/drafts": `{"drafts":[{"id":"r1","message":{"id":"m1"}}]}`,
	})

	drafts, err := newTestGmail(t, stub).ListDrafts(context.Background(), 5)
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}
	if drafts[0].To != "p@example.com" || drafts[0].Subject != "Запрос КП" {
		t.Errorf("draft: %+v", drafts[0])
	}
}
