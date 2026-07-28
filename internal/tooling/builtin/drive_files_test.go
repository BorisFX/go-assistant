package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

type fakeDrive struct {
	files      []gworkspace.FileInfo
	content    []byte
	resolved   string
	resolveErr error
	ensured    bool

	lastParent   string
	lastPath     string
	lastName     string
	lastMime     string
	lastUploaded []byte
}

func (f *fakeDrive) List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	f.lastParent = folderID
	return f.files, nil
}

func (f *fakeDrive) Search(ctx context.Context, text string) ([]gworkspace.FileInfo, error) {
	return f.files, nil
}

func (f *fakeDrive) Download(ctx context.Context, fileID string) ([]byte, error) {
	return f.content, nil
}

func (f *fakeDrive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error) {
	f.lastParent, f.lastName, f.lastMime, f.lastUploaded = parentID, name, mimeType, content
	return gworkspace.FileInfo{ID: "uploaded", Name: name, MimeType: mimeType}, nil
}

func (f *fakeDrive) EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error) {
	f.lastParent, f.lastName = parentID, name
	return gworkspace.FileInfo{ID: "folder", Name: name, IsFolder: true}, nil
}

func (f *fakeDrive) ResolvePath(ctx context.Context, path string) (string, error) {
	f.lastPath = path
	return f.resolved, f.resolveErr
}

func TestDriveFilesMetadata(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if tool.Name() != "drive_files" {
		t.Errorf("name: got %s", tool.Name())
	}
	if tool.Category() == "" {
		t.Error("category must not be empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestDriveFilesListResolvesPathFirst(t *testing.T) {
	fake := &fakeDrive{
		resolved: "folder-1",
		files:    []gworkspace.FileInfo{{ID: "a", Name: "договор.pdf", MimeType: "application/pdf"}},
	}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","path":"Ленина_42/10_Земля"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.lastPath != "Ленина_42/10_Земля" {
		t.Errorf("path handed to resolve: got %q", fake.lastPath)
	}
	if fake.lastParent != "folder-1" {
		t.Errorf("listing must use the resolved id, got %q", fake.lastParent)
	}

	var result struct {
		Files []gworkspace.FileInfo `json:"files"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
}

func TestDriveFilesListPropagatesResolveError(t *testing.T) {
	fake := &fakeDrive{resolveErr: errors.New("folder not found")}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","path":"Нет_такого"}`)); err == nil {
		t.Fatal("expected the resolve error to surface")
	}
}

func TestDriveFilesRead(t *testing.T) {
	fake := &fakeDrive{content: []byte("текст договора")}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read","file_id":"a"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if result.Content != "текст договора" {
		t.Errorf("content: got %q", result.Content)
	}
	if result.Truncated {
		t.Error("a short file must not be reported as truncated")
	}
}

func TestDriveFilesReadRequiresFileID(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read"}`)); err == nil {
		t.Fatal("expected an error when file_id is missing")
	}
}

func TestDriveFilesDownloadWritesFile(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeDrive{content: []byte("pdf bytes")}
	tool := builtin.NewDriveFiles(fake, dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"download","file_id":"a","name":"договор.pdf"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result struct {
		Path string `json:"path"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if result.Size != len("pdf bytes") {
		t.Errorf("size: got %d", result.Size)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("saved file unreadable: %v", err)
	}
	if string(data) != "pdf bytes" {
		t.Errorf("saved content: got %q", data)
	}
}

// A model-supplied name must not be able to write outside the files directory.
func TestDriveFilesDownloadStripsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeDrive{content: []byte("x")}
	tool := builtin.NewDriveFiles(fake, dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"download","file_id":"a","name":"../../etc/passwd"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if filepath.Dir(result.Path) != dir {
		t.Errorf("file escaped the files dir: %q", result.Path)
	}
}

func TestDriveFilesUpload(t *testing.T) {
	fake := &fakeDrive{resolved: "folder-1"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	if _, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"upload","path":"Ленина_42","name":"справка.md","content":"# Справка"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.lastParent != "folder-1" {
		t.Errorf("upload target: got %q", fake.lastParent)
	}
	if fake.lastMime != "text/markdown" {
		t.Errorf("md must map to text/markdown, got %q", fake.lastMime)
	}
	if string(fake.lastUploaded) != "# Справка" {
		t.Errorf("content: got %q", fake.lastUploaded)
	}
}

func TestDriveFilesUploadRequiresName(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"upload","content":"x"}`)); err == nil {
		t.Fatal("expected an error when name is missing")
	}
}

func TestDriveFilesMkdir(t *testing.T) {
	fake := &fakeDrive{resolved: "root-1"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"mkdir","path":"","name":"Мира_7"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.lastName != "Мира_7" {
		t.Errorf("folder name: got %q", fake.lastName)
	}

	var info gworkspace.FileInfo
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if !info.IsFolder {
		t.Error("result must be a folder")
	}
}

func TestDriveFilesUnknownAction(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"destroy"}`)); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}

func (f *fakeDrive) Move(ctx context.Context, fileID, newParentID string) (gworkspace.FileInfo, error) {
	f.lastParent = newParentID
	return gworkspace.FileInfo{ID: fileID, Name: "moved.pdf"}, nil
}

func TestDriveFilesMoveResolvesDestination(t *testing.T) {
	fake := &fakeDrive{resolved: "stage-3"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"move","file_id":"f1","path":"Vertex/03_Техпланы"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.lastPath != "Vertex/03_Техпланы" {
		t.Errorf("destination path: got %q", fake.lastPath)
	}
	if fake.lastParent != "stage-3" {
		t.Errorf("move must use the resolved folder id, got %q", fake.lastParent)
	}
	var result struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if result.To != "Vertex/03_Техпланы" {
		t.Errorf("result must name the destination, got %q", result.To)
	}
}

func TestDriveFilesMoveRequiresFileID(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"move","path":"Vertex"}`)); err == nil {
		t.Fatal("expected an error when file_id is missing")
	}
}

func (f *fakeDrive) EnsurePath(ctx context.Context, path string) (string, error) {
	f.lastPath = path
	f.ensured = true
	return f.resolved, f.resolveErr
}

// Writing actions must tolerate a stage folder that does not exist yet, because
// the tool loop runs a batch in parallel and mkdir may not have landed.
func TestDriveFilesMoveEnsuresDestination(t *testing.T) {
	fake := &fakeDrive{resolved: "stage"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	if _, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"move","file_id":"f1","path":"Vertex/05_Адреса"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.ensured {
		t.Error("move must ensure the destination path, not merely resolve it")
	}
}

// Reading must stay strict: listing a folder that is not there is a real error,
// and silently creating it would hide typos.
func TestDriveFilesListDoesNotCreateFolders(t *testing.T) {
	fake := &fakeDrive{resolved: "x"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	if _, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"list","path":"Vertex/НетТакой"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.ensured {
		t.Error("list must not create folders")
	}
}

func (f *fakeDrive) CreateDoc(ctx context.Context, parentID, name, text string) (gworkspace.FileInfo, error) {
	f.lastParent, f.lastName = parentID, name
	f.lastUploaded = []byte(text)
	return gworkspace.FileInfo{ID: "doc1", Name: name, MimeType: "application/vnd.google-apps.document"}, nil
}

func TestDriveFilesCreateDocReturnsLink(t *testing.T) {
	fake := &fakeDrive{resolved: "stage-1"}
	tool := builtin.NewDriveFiles(fake, t.TempDir())

	out, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"create_doc","path":"Vertex/01_Подготовка","name":"КП Vertex","content":"текст"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
	if result.URL != "https://docs.google.com/document/d/doc1/edit" {
		t.Errorf("a shareable link is the point of the action, got %q", result.URL)
	}
	if string(fake.lastUploaded) != "текст" {
		t.Errorf("content: got %q", fake.lastUploaded)
	}
}

func TestDriveFilesCreateDocRequiresContent(t *testing.T) {
	tool := builtin.NewDriveFiles(&fakeDrive{}, t.TempDir())

	if _, err := tool.Execute(context.Background(),
		json.RawMessage(`{"action":"create_doc","name":"КП","path":"Vertex"}`)); err == nil {
		t.Fatal("expected an error for empty content")
	}
}
