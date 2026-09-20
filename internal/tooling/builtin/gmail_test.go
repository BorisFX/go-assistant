package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeGmail struct {
	msgs      []gworkspace.Message
	one       gworkspace.Message
	drafts    []gworkspace.DraftInfo
	lastDraft gworkspace.Outgoing
	lastQuery string
	lastLimit int
	err       error
}

func (f *fakeGmail) Search(ctx context.Context, query string, limit int) ([]gworkspace.Message, error) {
	f.lastQuery, f.lastLimit = query, limit
	return f.msgs, f.err
}
func (f *fakeGmail) Get(ctx context.Context, id string) (gworkspace.Message, error) {
	return f.one, f.err
}
func (f *fakeGmail) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	return []byte("PDF-1"), f.err
}
func (f *fakeGmail) CreateDraft(ctx context.Context, out gworkspace.Outgoing) (gworkspace.DraftInfo, error) {
	if f.err != nil {
		return gworkspace.DraftInfo{}, f.err
	}
	if err := out.Validate(); err != nil {
		return gworkspace.DraftInfo{}, err
	}
	f.lastDraft = out
	return gworkspace.DraftInfo{ID: "r1", To: out.To[0], Subject: out.Subject}, nil
}

func (f *fakeGmail) ListDrafts(ctx context.Context, limit int) ([]gworkspace.DraftInfo, error) {
	return f.drafts, f.err
}

func (f *fakeGmail) User() string { return "info@samostrou.net" }

func TestGmailToolMetadata(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	if tool.Name() != "gmail" {
		t.Errorf("name: %s", tool.Name())
	}
	if !strings.Contains(tool.Description(), "info@samostrou.net") {
		t.Errorf("description must name the mailbox: %s", tool.Description())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestGmailToolSearch(t *testing.T) {
	fake := &fakeGmail{msgs: []gworkspace.Message{{ID: "m1", Subject: "КП"}}}
	tool := builtin.NewGmail(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"has:attachment"}`))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(string(out), "m1") {
		t.Errorf("result: %s", out)
	}
	if fake.lastLimit != 10 {
		t.Errorf("default limit: %d", fake.lastLimit)
	}
}

func TestGmailToolSearchRequiresQuery(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"search"}`)); err == nil {
		t.Error("expected an error without a query")
	}
}

// A forwarded thread can be hundreds of kilobytes; the tool result must not be.
func TestGmailToolReadTruncatesBody(t *testing.T) {
	fake := &fakeGmail{one: gworkspace.Message{ID: "m1", Body: strings.Repeat("а", 20000)}}
	tool := builtin.NewGmail(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read","message_id":"m1"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "обрезан") {
		t.Errorf("long body not truncated: %d bytes", len(out))
	}
}

func TestGmailToolDownloadWritesFile(t *testing.T) {
	dir := t.TempDir()
	tool := builtin.NewGmail(&fakeGmail{}, dir)

	out, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"download","message_id":"m1","attachment_id":"a1","name":"Уведомление.pdf"}`))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	var res struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("result: %v", err)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil || string(data) != "PDF-1" {
		t.Fatalf("file: %v %q", err, data)
	}
	if !strings.HasPrefix(res.Path, dir) {
		t.Errorf("attachment written outside files dir: %s", res.Path)
	}
}

// An attachment name is attacker-controlled: it must not walk out of filesDir.
func TestGmailToolDownloadSanitizesName(t *testing.T) {
	dir := t.TempDir()
	tool := builtin.NewGmail(&fakeGmail{}, dir)

	out, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"download","message_id":"m1","attachment_id":"a1","name":"../../etc/passwd"}`))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if strings.Contains(string(out), "..") {
		t.Errorf("path traversal survived: %s", out)
	}
}

func TestGmailToolSurfacesClientErrors(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{err: errors.New("403 delegation denied")}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read","message_id":"m1"}`)); err == nil {
		t.Error("expected the client error to surface")
	}
}

func TestGmailToolUnknownAction(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send"}`)); err == nil {
		t.Error("send must not be silently accepted")
	}
}

func TestGmailToolDraftAsksForConfirmation(t *testing.T) {
	fake := &fakeGmail{}
	tool := builtin.NewGmail(fake, t.TempDir())

	var carded gworkspace.DraftInfo
	tool.SetOnDraft(func(info gworkspace.DraftInfo) { carded = info })

	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"draft","to":["p@example.com"],"subject":"Запрос КП","body":"ТЗ во вложении"}`))
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if fake.lastDraft.Subject != "Запрос КП" {
		t.Errorf("draft not built: %+v", fake.lastDraft)
	}
	if carded.ID != "r1" {
		t.Error("confirmation card was not requested")
	}
	if !strings.Contains(string(out), "НЕ отправлен") {
		t.Errorf("result must tell the model the letter is not sent: %s", out)
	}
}

// The model must not be able to send by itself, under any action name.
func TestGmailToolHasNoSendAction(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	if strings.Contains(string(tool.Schema()), `"send"`) {
		t.Error("schema offers a send action")
	}
	for _, action := range []string{"send", "send_draft", "drafts.send"} {
		params := json.RawMessage(`{"action":"` + action + `","draft_id":"r1"}`)
		if _, err := tool.Execute(context.Background(), params); err == nil {
			t.Errorf("action %q was accepted", action)
		}
	}
}

func TestGmailToolDraftValidates(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	for _, params := range []string{
		`{"action":"draft","subject":"Тема","body":"текст"}`,
		`{"action":"draft","to":["не адрес"],"subject":"Тема","body":"текст"}`,
		`{"action":"draft","to":["p@example.com"],"body":"текст"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err == nil {
			t.Errorf("accepted a broken draft: %s", params)
		}
	}
}

func TestGmailToolAttachesLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ТЗ.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGmail{}
	tool := builtin.NewGmail(fake, dir)

	params := `{"action":"draft","to":["p@example.com"],"subject":"ТЗ","body":"во вложении","attachments":["` + path + `"]}`
	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err != nil {
		t.Fatalf("draft: %v", err)
	}
	atts := fake.lastDraft.Attachments
	if len(atts) != 1 || atts[0].Name != "ТЗ.pdf" || string(atts[0].Content) != "%PDF-1.4" {
		t.Fatalf("attachment: %+v", atts)
	}
	if atts[0].MimeType != "application/pdf" {
		t.Errorf("mime type: %q", atts[0].MimeType)
	}
}

// Attachment paths come from model output: a letter must never be able to carry
// a file from outside the bot's own directory.
func TestGmailToolRefusesAttachmentOutsideFilesDir(t *testing.T) {
	tool := builtin.NewGmail(&fakeGmail{}, t.TempDir())

	params := `{"action":"draft","to":["p@example.com"],"subject":"Тема","body":"текст","attachments":["/etc/passwd"]}`
	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err == nil {
		t.Fatal("a file outside the files dir was attached")
	}
}

func TestGmailToolListsDrafts(t *testing.T) {
	fake := &fakeGmail{drafts: []gworkspace.DraftInfo{{ID: "r1", Subject: "Запрос КП"}}}
	tool := builtin.NewGmail(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"drafts"}`))
	if err != nil {
		t.Fatalf("drafts: %v", err)
	}
	if !strings.Contains(string(out), "Запрос КП") {
		t.Errorf("result: %s", out)
	}
}

// Модель помнит имя файла, но путает каталог: ТЗ лежит в files/pdf/, а просят
// из files/. Рассылка из-за этого разваливаться не должна.
func TestGmailToolFindsAttachmentByName(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "pdf")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(nested, "ТЗ.pdf")
	if err := os.WriteFile(real, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGmail{}
	tool := builtin.NewGmail(fake, dir)

	wrong := filepath.Join(dir, "ТЗ.pdf") // каталогом выше, чем на самом деле
	params := `{"action":"draft","to":["p@example.com"],"subject":"ТЗ","body":"во вложении","attachments":["` + wrong + `"]}`
	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if len(fake.lastDraft.Attachments) != 1 || string(fake.lastDraft.Attachments[0].Content) != "%PDF-1.4" {
		t.Fatalf("вложение не подхвачено: %+v", fake.lastDraft.Attachments)
	}
}

// Если файла нет нигде — ошибка должна называть то, что есть.
func TestGmailToolListsAvailableFilesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Смета.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := builtin.NewGmail(&fakeGmail{}, dir)

	params := `{"action":"draft","to":["p@example.com"],"subject":"ТЗ","body":"текст","attachments":["` + filepath.Join(dir, "Нет.pdf") + `"]}`
	_, err := tool.Execute(context.Background(), json.RawMessage(params))
	if err == nil || !strings.Contains(err.Error(), "Смета.pdf") {
		t.Fatalf("ошибка не подсказывает доступные файлы: %v", err)
	}
}
