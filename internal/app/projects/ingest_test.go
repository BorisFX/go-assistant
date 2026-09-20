package projects_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

type fakeMail struct {
	heads    []gworkspace.Message
	messages map[string]gworkspace.Message
	labelled []string
	labelErr error
	getErr   map[string]error
	dlErr    error
}

func (f *fakeMail) ListIDs(ctx context.Context, query string, limit int) ([]string, error) {
	ids := make([]string, 0, len(f.heads))
	for _, m := range f.heads {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func (f *fakeMail) Get(ctx context.Context, id string) (gworkspace.Message, error) {
	if err := f.getErr[id]; err != nil {
		return gworkspace.Message{}, err
	}
	return f.messages[id], nil
}

func (f *fakeMail) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	if f.dlErr != nil {
		return nil, f.dlErr
	}
	return []byte("bytes of " + attachmentID), nil
}

func (f *fakeMail) EnsureLabel(ctx context.Context, name string) (string, error) {
	if f.labelErr != nil {
		return "", f.labelErr
	}
	return "Label_1", nil
}

func (f *fakeMail) AddLabel(ctx context.Context, messageID, labelID string) error {
	f.labelled = append(f.labelled, messageID)
	return nil
}

type fakeMailDrive struct {
	folders  map[string][]gworkspace.FileInfo // path -> files already in the folder
	uploaded map[string][]string              // path -> uploaded file names
	ensured  []string
}

func newFakeMailDrive() *fakeMailDrive {
	return &fakeMailDrive{
		folders:  map[string][]gworkspace.FileInfo{},
		uploaded: map[string][]string{},
	}
}

func (f *fakeMailDrive) EnsurePath(ctx context.Context, path string) (string, error) {
	f.ensured = append(f.ensured, path)
	return path, nil
}

func (f *fakeMailDrive) List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	return f.folders[folderID], nil
}

func (f *fakeMailDrive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error) {
	f.uploaded[parentID] = append(f.uploaded[parentID], name)
	return gworkspace.FileInfo{ID: parentID + "/" + name, Name: name}, nil
}

func letter(id, subject, body string, atts ...gworkspace.Attachment) gworkspace.Message {
	return gworkspace.Message{
		ID: id, ThreadID: "t" + id, Subject: subject, Body: body,
		From: "p@example.com", Date: "2026-09-17T08:30:00Z", Attachments: atts,
	}
}

func newCourier(mail *fakeMail, drive *fakeMailDrive) *projects.Courier {
	svc, _, _ := newSvc(map[string][]string{"Vertex": {}})
	return projects.NewCourier(mail, drive, svc, projects.CourierConfig{
		Query: "in:inbox", Label: "Обработано", MaxMessages: 20,
	}, nil)
}

func TestCourierFilesAttachmentsUnderProject(t *testing.T) {
	mail := &fakeMail{
		heads: []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{
			"m1": letter("m1", "Техплан по Vertex", "во вложении",
				gworkspace.Attachment{ID: "a1", Name: "Техплан.pdf", MimeType: "application/pdf", Size: 900000}),
		},
	}
	drive := newFakeMailDrive()

	deliveries, err := newCourier(mail, drive).Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries))
	}
	d := deliveries[0]
	if d.Project != "Vertex" {
		t.Errorf("project: %q", d.Project)
	}
	want := "Vertex/99_Переписка/2026-09-17_Техплан по Vertex"
	if d.Folder != want {
		t.Errorf("folder: got %q, want %q", d.Folder, want)
	}
	if got := drive.uploaded[want]; len(got) != 1 || got[0] != "Техплан.pdf" {
		t.Errorf("uploaded: %v", got)
	}
	if len(mail.labelled) != 1 {
		t.Errorf("message must be labelled once handled: %v", mail.labelled)
	}
}

func TestCourierRoutesUnknownProjectToUnsorted(t *testing.T) {
	mail := &fakeMail{
		heads: []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{
			"m1": letter("m1", "Счёт на оплату", "реквизиты",
				gworkspace.Attachment{ID: "a1", Name: "Счёт.pdf", MimeType: "application/pdf", Size: 1000}),
		},
	}
	drive := newFakeMailDrive()

	deliveries, _ := newCourier(mail, drive).Poll(context.Background())
	if got := deliveries[0].Folder; !strings.HasPrefix(got, "_Разобрать/") {
		t.Errorf("folder: %q", got)
	}
}

// A signature logo is not a document; a photographed plan is.
func TestCourierSkipsSignatureImages(t *testing.T) {
	mail := &fakeMail{
		heads: []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{
			"m1": letter("m1", "Vertex, ответ", "",
				gworkspace.Attachment{ID: "a1", Name: "logo.png", MimeType: "image/png", Size: 4096},
				gworkspace.Attachment{ID: "a2", Name: "план.jpg", MimeType: "image/jpeg", Size: 2_400_000}),
		},
	}
	drive := newFakeMailDrive()

	deliveries, _ := newCourier(mail, drive).Poll(context.Background())
	saved := deliveries[0].Saved
	if len(saved) != 1 || saved[0] != "план.jpg" {
		t.Errorf("saved: %v", saved)
	}
}

// A letter with nothing attached is marked handled and reported as a count, not
// as a folder the user is asked to review.
func TestCourierLabelsLetterWithoutAttachments(t *testing.T) {
	mail := &fakeMail{
		heads:    []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{"m1": letter("m1", "Vertex, созвон", "в 15:00")},
	}
	drive := newFakeMailDrive()

	deliveries, _ := newCourier(mail, drive).Poll(context.Background())
	if len(deliveries[0].Saved) != 0 || deliveries[0].Err != nil {
		t.Errorf("delivery: %+v", deliveries[0])
	}
	if len(mail.labelled) != 1 {
		t.Errorf("labelled: %v", mail.labelled)
	}
	if len(drive.ensured) != 0 {
		t.Errorf("no folder should be created for an empty letter: %v", drive.ensured)
	}
}

// Re-running after a crash between upload and labelling must not duplicate files.
func TestCourierSkipsFilesAlreadyInFolder(t *testing.T) {
	folder := "Vertex/99_Переписка/2026-09-17_Техплан по Vertex"
	mail := &fakeMail{
		heads: []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{
			"m1": letter("m1", "Техплан по Vertex", "",
				gworkspace.Attachment{ID: "a1", Name: "Техплан.pdf", MimeType: "application/pdf", Size: 900000}),
		},
	}
	drive := newFakeMailDrive()
	drive.folders[folder] = []gworkspace.FileInfo{{Name: "Техплан.pdf", Size: 900000}}

	deliveries, _ := newCourier(mail, drive).Poll(context.Background())
	if len(drive.uploaded[folder]) != 0 {
		t.Errorf("re-uploaded: %v", drive.uploaded[folder])
	}
	if deliveries[0].Err != nil {
		t.Errorf("a repeat delivery is not a failure: %v", deliveries[0].Err)
	}
}

// Two contractors answering the same tender both attach "КП.pdf"; neither copy
// may quietly overwrite or swallow the other.
func TestCourierKeepsBothFilesWithTheSameName(t *testing.T) {
	folder := "Vertex/99_Переписка/2026-09-17_КП по Vertex"
	mail := &fakeMail{
		heads: []gworkspace.Message{{ID: "m1"}},
		messages: map[string]gworkspace.Message{
			"m1": letter("m1", "КП по Vertex", "",
				gworkspace.Attachment{ID: "a1", Name: "КП.pdf", MimeType: "application/pdf", Size: 44000}),
		},
	}
	drive := newFakeMailDrive()
	drive.folders[folder] = []gworkspace.FileInfo{{Name: "КП.pdf", Size: 12000}}

	deliveries, _ := newCourier(mail, drive).Poll(context.Background())
	if got := drive.uploaded[folder]; len(got) != 1 || got[0] != "КП (2).pdf" {
		t.Errorf("uploaded: %v", got)
	}
	if saved := deliveries[0].Saved; len(saved) != 1 || saved[0] != "КП (2).pdf" {
		t.Errorf("saved: %v", saved)
	}
}

// One bad message must not stop the batch, and must stay unlabelled so the next
// pass retries it.
func TestCourierContinuesAfterFailure(t *testing.T) {
	mail := &fakeMail{
		heads:  []gworkspace.Message{{ID: "m1"}, {ID: "m2"}},
		getErr: map[string]error{"m1": errors.New("gmail 503")},
		messages: map[string]gworkspace.Message{
			"m2": letter("m2", "Vertex акты", "",
				gworkspace.Attachment{ID: "a2", Name: "КС-2.pdf", MimeType: "application/pdf", Size: 50000}),
		},
	}
	drive := newFakeMailDrive()

	deliveries, err := newCourier(mail, drive).Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(deliveries) != 2 || deliveries[0].Err == nil {
		t.Fatalf("deliveries: %+v", deliveries)
	}
	if len(mail.labelled) != 1 || mail.labelled[0] != "m2" {
		t.Errorf("only the succeeded message may be labelled: %v", mail.labelled)
	}
}

// Without a label there is no dedup, so the pass must not start at all.
func TestCourierStopsWhenLabelUnavailable(t *testing.T) {
	mail := &fakeMail{labelErr: errors.New("no permission")}
	if _, err := newCourier(mail, newFakeMailDrive()).Poll(context.Background()); err == nil {
		t.Fatal("expected an error when the label cannot be ensured")
	}
}

func TestFormatDigestNamesTheReviewCommand(t *testing.T) {
	text := projects.FormatDigest([]projects.Delivery{
		{Subject: "Техплан", Project: "Vertex", Folder: "Vertex/99_Переписка/2026-09-17_Техплан",
			Saved: []string{"a.pdf", "b.pdf"}},
		{Subject: "Пусто"},
		{Subject: "Сбой", Err: errors.New("boom")},
	})

	if !strings.Contains(text, "разбери папку Vertex/99_Переписка/2026-09-17_Техплан") {
		t.Errorf("digest must spell out the review command:\n%s", text)
	}
	if !strings.Contains(text, "Без вложений: 1") || !strings.Contains(text, "Не разобрано: 1") {
		t.Errorf("digest counts:\n%s", text)
	}
}

func TestFormatDigestStaysQuietWhenNothingHappened(t *testing.T) {
	if text := projects.FormatDigest(nil); text != "" {
		t.Errorf("expected silence, got %q", text)
	}
}
