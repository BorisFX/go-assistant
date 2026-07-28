package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"google.golang.org/api/option"
)

// driveStub replays canned responses in order and records every query, so
// tests can assert on the requests the adapter builds.
type driveStub struct {
	*httptest.Server
	responses []string
	queries   []url.Values
	n         int
}

func newDriveStub(t *testing.T, responses ...string) *driveStub {
	t.Helper()
	s := &driveStub{responses: responses}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.queries = append(s.queries, r.URL.Query())
		body := `{}`
		if s.n < len(s.responses) {
			body = s.responses[s.n]
		}
		s.n++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *driveStub) lastQuery() url.Values {
	if len(s.queries) == 0 {
		return url.Values{}
	}
	return s.queries[len(s.queries)-1]
}

func newTestDrive(t *testing.T, stub *driveStub) *gworkspace.Drive {
	t.Helper()
	d, err := gworkspace.NewDrive(context.Background(), "0ADRIVE",
		option.WithoutAuthentication(),
		option.WithEndpoint(stub.URL+"/"),
	)
	if err != nil {
		t.Fatalf("new drive: %v", err)
	}
	return d
}

func TestDriveRequiresRootFolder(t *testing.T) {
	if _, err := gworkspace.NewDrive(context.Background(), "", option.WithoutAuthentication()); err == nil {
		t.Fatal("expected an error when root folder id is empty")
	}
}

func TestDriveListParsesFiles(t *testing.T) {
	stub := newDriveStub(t, `{"files":[
		{"id":"f1","name":"10_Земля","mimeType":"application/vnd.google-apps.folder"},
		{"id":"f2","name":"договор.pdf","mimeType":"application/pdf","size":"1024"}
	]}`)

	files, err := newTestDrive(t, stub).List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !files[0].IsFolder {
		t.Error("a folder mime type must set IsFolder")
	}
	if files[1].IsFolder {
		t.Error("a pdf must not be reported as a folder")
	}
	if files[1].Size != 1024 {
		t.Errorf("size: got %d", files[1].Size)
	}
}

// Without these parameters the API answers with an empty list instead of an
// error, so their presence is a correctness requirement, not a detail.
func TestDriveListSendsSharedDriveParams(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).List(context.Background(), ""); err != nil {
		t.Fatalf("list: %v", err)
	}

	q := stub.lastQuery()
	for _, c := range []struct{ key, want string }{
		{"supportsAllDrives", "true"},
		{"includeItemsFromAllDrives", "true"},
		{"corpora", "drive"},
		{"driveId", "0ADRIVE"},
	} {
		if got := q.Get(c.key); got != c.want {
			t.Errorf("%s: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestDriveListRootUsesDriveIDAsParent(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).List(context.Background(), ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := stub.lastQuery().Get("q"); got != "'0ADRIVE' in parents and trashed = false" {
		t.Errorf("query: got %q", got)
	}
}

// A quote in a file name would otherwise terminate the query literal and make
// the API reject the whole request.
func TestDriveSearchEscapesQuotes(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).Search(context.Background(), "О'Кей"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := stub.lastQuery().Get("q"); !strings.Contains(got, `О\'Кей`) {
		t.Errorf("quote must be escaped, got %q", got)
	}
}

func TestDriveSearchRejectsEmptyQuery(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)
	if _, err := newTestDrive(t, stub).Search(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for an empty search query")
	}
}

// Scaffolding re-runs after partial failures, so an existing folder must be
// reused rather than duplicated.
func TestDriveEnsureFolderReusesExisting(t *testing.T) {
	stub := newDriveStub(t, `{"files":[{"id":"existing","name":"10_Земля","mimeType":"application/vnd.google-apps.folder"}]}`)

	info, err := newTestDrive(t, stub).EnsureFolder(context.Background(), "", "10_Земля")
	if err != nil {
		t.Fatalf("ensure folder: %v", err)
	}
	if info.ID != "existing" {
		t.Errorf("id: got %q, want existing", info.ID)
	}
	if len(stub.queries) != 1 {
		t.Errorf("expected only a lookup, got %d requests", len(stub.queries))
	}
}

func TestDriveEnsureFolderCreatesWhenMissing(t *testing.T) {
	stub := newDriveStub(t,
		`{"files":[]}`,
		`{"id":"created","name":"10_Земля","mimeType":"application/vnd.google-apps.folder"}`,
	)

	info, err := newTestDrive(t, stub).EnsureFolder(context.Background(), "", "10_Земля")
	if err != nil {
		t.Fatalf("ensure folder: %v", err)
	}
	if info.ID != "created" {
		t.Errorf("id: got %q, want created", info.ID)
	}
	if !info.IsFolder {
		t.Error("created entry must be a folder")
	}
}

func TestDriveResolvePathWalksFolders(t *testing.T) {
	stub := newDriveStub(t,
		`{"files":[{"id":"project","name":"Ленина_42","mimeType":"application/vnd.google-apps.folder"}]}`,
		`{"files":[{"id":"section","name":"10_Земля","mimeType":"application/vnd.google-apps.folder"}]}`,
	)

	id, err := newTestDrive(t, stub).ResolvePath(context.Background(), "Ленина_42/10_Земля")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "section" {
		t.Errorf("id: got %q, want section", id)
	}
	if len(stub.queries) != 2 {
		t.Fatalf("expected 2 lookups, got %d", len(stub.queries))
	}
	if !strings.Contains(stub.queries[1].Get("q"), "'project' in parents") {
		t.Errorf("second lookup must descend into the first: %q", stub.queries[1].Get("q"))
	}
}

func TestDriveResolvePathEmptyIsRoot(t *testing.T) {
	stub := newDriveStub(t)

	id, err := newTestDrive(t, stub).ResolvePath(context.Background(), "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "0ADRIVE" {
		t.Errorf("empty path must resolve to the drive root, got %q", id)
	}
	if len(stub.queries) != 0 {
		t.Errorf("root needs no lookup, got %d requests", len(stub.queries))
	}
}

func TestDriveResolvePathMissingFolder(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)

	if _, err := newTestDrive(t, stub).ResolvePath(context.Background(), "Нет_такого"); err == nil {
		t.Fatal("expected an error for a missing folder")
	}
}

// Sorting documents into stage folders is the whole point of the workspace, so
// move is a first-class operation rather than delete-and-reupload.
func TestDriveMoveReparentsFile(t *testing.T) {
	stub := newDriveStub(t,
		`{"id":"f1","parents":["old-parent"]}`,
		`{"id":"f1","name":"РС 1.pdf","mimeType":"application/pdf"}`,
	)

	info, err := newTestDrive(t, stub).Move(context.Background(), "f1", "new-parent")
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if info.ID != "f1" {
		t.Errorf("id: got %q", info.ID)
	}
	if len(stub.queries) != 2 {
		t.Fatalf("expected a lookup then an update, got %d requests", len(stub.queries))
	}

	q := stub.queries[1]
	if q.Get("addParents") != "new-parent" {
		t.Errorf("addParents: got %q", q.Get("addParents"))
	}
	if q.Get("removeParents") != "old-parent" {
		t.Errorf("removeParents: got %q", q.Get("removeParents"))
	}
	if q.Get("supportsAllDrives") != "true" {
		t.Error("supportsAllDrives must be set on a shared drive")
	}
}

func TestDriveMoveRequiresIDs(t *testing.T) {
	stub := newDriveStub(t)
	d := newTestDrive(t, stub)

	if _, err := d.Move(context.Background(), "", "p"); err == nil {
		t.Error("expected an error for an empty file id")
	}
	if _, err := d.Move(context.Background(), "f", ""); err == nil {
		t.Error("expected an error for an empty target folder")
	}
}

// EnsurePath removes the ordering dependency between mkdir and move: the tool
// loop runs a batch of calls in parallel, so "create then fill" cannot be
// relied on. The first segment is deliberately NOT created — a typo in the
// project name must fail loudly instead of silently starting a new project.
func TestEnsurePathCreatesMissingStageFolder(t *testing.T) {
	stub := newDriveStub(t,
		`{"files":[{"id":"proj","name":"Vertex","mimeType":"application/vnd.google-apps.folder"}]}`,
		`{"files":[]}`,
		`{"id":"stage","name":"05_Адреса","mimeType":"application/vnd.google-apps.folder"}`,
	)

	id, err := newTestDrive(t, stub).EnsurePath(context.Background(), "Vertex/05_Адреса")
	if err != nil {
		t.Fatalf("ensure path: %v", err)
	}
	if id != "stage" {
		t.Errorf("id: got %q, want stage", id)
	}
}

func TestEnsurePathRejectsUnknownProject(t *testing.T) {
	stub := newDriveStub(t, `{"files":[]}`)

	if _, err := newTestDrive(t, stub).EnsurePath(context.Background(), "Опечатка/05_Адреса"); err == nil {
		t.Fatal("a missing first segment must fail, not be created")
	}
	if len(stub.queries) != 1 {
		t.Errorf("must stop after the failed lookup, got %d requests", len(stub.queries))
	}
}

func TestEnsurePathEmptyIsRoot(t *testing.T) {
	stub := newDriveStub(t)

	id, err := newTestDrive(t, stub).EnsurePath(context.Background(), "")
	if err != nil {
		t.Fatalf("ensure path: %v", err)
	}
	if id != "0ADRIVE" {
		t.Errorf("got %q, want the drive root", id)
	}
}

func TestEnsurePathExistingChainCreatesNothing(t *testing.T) {
	stub := newDriveStub(t,
		`{"files":[{"id":"proj","name":"Vertex","mimeType":"application/vnd.google-apps.folder"}]}`,
		`{"files":[{"id":"stage","name":"08_Аренда","mimeType":"application/vnd.google-apps.folder"}]}`,
	)

	id, err := newTestDrive(t, stub).EnsurePath(context.Background(), "Vertex/08_Аренда")
	if err != nil {
		t.Fatalf("ensure path: %v", err)
	}
	if id != "stage" {
		t.Errorf("id: got %q", id)
	}
	if len(stub.queries) != 2 {
		t.Errorf("expected two lookups and no creation, got %d requests", len(stub.queries))
	}
}

// Google-native files have no bytes to download — the API refuses and points at
// export. Templates and the roadmap live as Docs, so reading them is not an
// edge case but the main path.
func TestDriveDownloadExportsGoogleDoc(t *testing.T) {
	stub := newDriveStub(t,
		`{"id":"doc1","mimeType":"application/vnd.google-apps.document"}`,
		`текст шаблона`,
	)

	data, err := newTestDrive(t, stub).Download(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(data) != "текст шаблона" {
		t.Errorf("content: got %q", data)
	}
	if got := stub.queries[1].Get("mimeType"); got != "text/plain" {
		t.Errorf("a document must be exported as text/plain, got %q", got)
	}
}

func TestDriveDownloadExportsSpreadsheetAsCSV(t *testing.T) {
	stub := newDriveStub(t,
		`{"id":"sh1","mimeType":"application/vnd.google-apps.spreadsheet"}`,
		"a,b\n1,2",
	)

	if _, err := newTestDrive(t, stub).Download(context.Background(), "sh1"); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got := stub.queries[1].Get("mimeType"); got != "text/csv" {
		t.Errorf("a spreadsheet must be exported as csv, got %q", got)
	}
}

func TestDriveDownloadKeepsBinaryPath(t *testing.T) {
	stub := newDriveStub(t,
		`{"id":"p1","mimeType":"application/pdf"}`,
		`%PDF-1.4`,
	)

	data, err := newTestDrive(t, stub).Download(context.Background(), "p1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(data) != "%PDF-1.4" {
		t.Errorf("content: got %q", data)
	}
	if stub.queries[1].Get("mimeType") != "" {
		t.Error("a pdf must be downloaded, not exported")
	}
}

// A commercial proposal is sent to a client, so it must be a real document they
// can open and comment on — not a text file sitting in a folder.
func TestDriveCreateDocConvertsToGoogleDoc(t *testing.T) {
	stub := newDriveStub(t, `{"id":"doc1","name":"КП Vertex","mimeType":"application/vnd.google-apps.document"}`)

	info, err := newTestDrive(t, stub).CreateDoc(context.Background(), "folder-1", "КП Vertex", "текст предложения")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if info.ID != "doc1" {
		t.Errorf("id: got %q", info.ID)
	}
	if info.MimeType != "application/vnd.google-apps.document" {
		t.Errorf("must be a Google Doc, got %q", info.MimeType)
	}
}

func TestDriveCreateDocRequiresName(t *testing.T) {
	stub := newDriveStub(t)
	if _, err := newTestDrive(t, stub).CreateDoc(context.Background(), "f", "", "x"); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}
