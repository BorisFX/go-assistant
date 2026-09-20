package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

// importDrive records what was created and uploaded, and serves folders back so
// the second run of an import sees what the first one wrote.
type importDrive struct {
	folders  map[string][]gworkspace.FileInfo // folder id -> contents
	created  []string
	uploaded []string
	failOn   string
}

func newImportDrive() *importDrive {
	return &importDrive{folders: map[string][]gworkspace.FileInfo{}}
}

func (d *importDrive) List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	return d.folders[folderID], nil
}
func (d *importDrive) Search(ctx context.Context, text string) ([]gworkspace.FileInfo, error) {
	return nil, nil
}
func (d *importDrive) Download(ctx context.Context, fileID string) ([]byte, error) {
	return nil, nil
}
func (d *importDrive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error) {
	if name == d.failOn {
		return gworkspace.FileInfo{}, errors.New("квота исчерпана")
	}
	d.uploaded = append(d.uploaded, parentID+"/"+name)
	d.folders[parentID] = append(d.folders[parentID],
		gworkspace.FileInfo{Name: name, Size: int64(len(content))})
	return gworkspace.FileInfo{ID: parentID + "/" + name, Name: name}, nil
}

// EnsureFolder keys a folder by its full path, so the mirrored tree is visible
// in the ids themselves.
func (d *importDrive) EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error) {
	id := name
	if parentID != "" {
		id = parentID + "/" + name
	}
	if _, seen := d.folders[id]; !seen {
		d.folders[id] = nil
		d.created = append(d.created, id)
	}
	return gworkspace.FileInfo{ID: id, Name: name, IsFolder: true}, nil
}
func (d *importDrive) Move(ctx context.Context, fileID, newParentID string) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *importDrive) CreateDoc(ctx context.Context, parentID, name, text string) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *importDrive) ResolvePath(ctx context.Context, path string) (string, error) {
	return path, nil
}
func (d *importDrive) EnsurePath(ctx context.Context, path string) (string, error) {
	return path, nil
}

// fakeCloud stands in for Mail.ru: it returns files already sitting in the local
// cache, which is exactly what CollectFolder does.
type fakeCloud struct {
	files      []string
	err        error
	publicName string
	publicHits int
}

func (c *fakeCloud) CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error) {
	return c.files, c.err
}

func (c *fakeCloud) CollectPublic(ctx context.Context, link string, exts []string, maxFiles int) ([]string, error) {
	c.publicHits++
	return c.files, c.err
}

func (c *fakeCloud) PublicFolderName(ctx context.Context, token string) (string, error) {
	if c.publicName == "" {
		return "Публичная папка", nil
	}
	return c.publicName, nil
}

// runImport starts the detached migration and waits for its report, which is the
// only thing the user ever sees of it.
func runImport(t *testing.T, tool *builtin.DriveFiles, params string) string {
	t.Helper()
	done := make(chan string, 1)
	tool.SetNotify(func(s string) { done <- s })

	if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err != nil {
		t.Fatalf("import: %v", err)
	}
	select {
	case summary := <-done:
		return summary
	case <-time.After(5 * time.Second):
		t.Fatal("отчёт о переносе не пришёл")
		return ""
	}
}

// cloudCache writes files into filesDir/cloud/<rel>, mirroring the real cache.
func cloudCache(t *testing.T, filesDir string, files map[string]string) []string {
	t.Helper()
	var paths []string
	for rel, content := range files {
		full := filepath.Join(filesDir, "cloud", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}
	return paths
}

func TestImportCloudKeepsFolderStructure(t *testing.T) {
	dir := t.TempDir()
	files := cloudCache(t, dir, map[string]string{
		"СОЛОЩУК 11/Техплан.pdf":           "%PDF техплан",
		"СОЛОЩУК 11/ЕГРН/Выписка.pdf":      "%PDF выписка",
		"СОЛОЩУК 11/ЕГРН/Приостановка.pdf": "%PDF приостановка",
	})
	drive := newImportDrive()
	tool := builtin.NewDriveFiles(drive, dir)
	tool.SetCloudSource(&fakeCloud{files: files})

	summary := runImport(t, tool,
		`{"action":"import_cloud","cloud_path":"/НОГИНСК/СОЛОЩУК 11","path":"Солощук_11/00_Исходные"}`)
	if len(drive.uploaded) != 3 {
		t.Fatalf("uploaded: %v", drive.uploaded)
	}
	// The project folder is created top level and down, and the cloud subfolder
	// is reproduced instead of being flattened.
	joined := strings.Join(drive.created, "|")
	for _, want := range []string{"Солощук_11", "Солощук_11/00_Исходные", "Солощук_11/00_Исходные/СОЛОЩУК 11/ЕГРН"} {
		if !strings.Contains(joined, want) {
			t.Errorf("folder %q not created; created: %v", want, drive.created)
		}
	}
	if !strings.Contains(summary, "Загружено: 3") {
		t.Errorf("отчёт: %s", summary)
	}
}

// Running the same migration twice must not double every document.
func TestImportCloudSkipsWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	files := cloudCache(t, dir, map[string]string{"Объект/Техплан.pdf": "%PDF"})
	drive := newImportDrive()
	tool := builtin.NewDriveFiles(drive, dir)
	tool.SetCloudSource(&fakeCloud{files: files})
	params := `{"action":"import_cloud","cloud_path":"/Объект","path":"Объект"}`

	runImport(t, tool, params)
	summary := runImport(t, tool, params)

	if len(drive.uploaded) != 1 {
		t.Errorf("document uploaded twice: %v", drive.uploaded)
	}
	if !strings.Contains(summary, "Уже было на месте: 1") {
		t.Errorf("повтор не отмечен: %s", summary)
	}
}

// One failed upload must not abort the migration, and must be named.
func TestImportCloudReportsFailuresPerFile(t *testing.T) {
	dir := t.TempDir()
	files := cloudCache(t, dir, map[string]string{
		"Объект/Хороший.pdf": "ok",
		"Объект/Плохой.pdf":  "bad",
	})
	drive := newImportDrive()
	drive.failOn = "Плохой.pdf"
	tool := builtin.NewDriveFiles(drive, dir)
	tool.SetCloudSource(&fakeCloud{files: files})

	summary := runImport(t, tool, `{"action":"import_cloud","cloud_path":"/Объект","path":"Объект"}`)

	if len(drive.uploaded) != 1 {
		t.Errorf("uploaded: %v", drive.uploaded)
	}
	if !strings.Contains(summary, "Плохой.pdf") || !strings.Contains(summary, "квота") {
		t.Errorf("сбой не назван: %s", summary)
	}
}

func TestImportCloudRequiresSource(t *testing.T) {
	tool := builtin.NewDriveFiles(newImportDrive(), t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"import_cloud","cloud_path":"/Объект","path":"Объект"}`))
	if err == nil || !strings.Contains(err.Error(), "Mail.ru") {
		t.Fatalf("expected a clear error without a cloud source, got %v", err)
	}
}

func TestImportCloudValidatesParams(t *testing.T) {
	dir := t.TempDir()
	tool := builtin.NewDriveFiles(newImportDrive(), dir)
	tool.SetCloudSource(&fakeCloud{})

	for _, params := range []string{
		`{"action":"import_cloud","path":"Объект"}`,
		`{"action":"import_cloud","cloud_path":"/Объект без ссылки и без назначения"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(params)); err == nil {
			t.Errorf("accepted %s", params)
		}
	}
}

// A share link is what Yuri actually sends: the folder sits in someone else's
// cloud, so there is no path inside the bot's own account to name.
func TestImportCloudAcceptsPublicLink(t *testing.T) {
	dir := t.TempDir()
	files := cloudCache(t, dir, map[string]string{"Доки/Техплан.pdf": "%PDF"})
	drive := newImportDrive()
	cloud := &fakeCloud{files: files, publicName: "Пирогово пристройка Пшенников ДА"}
	tool := builtin.NewDriveFiles(drive, dir)
	tool.SetCloudSource(cloud)

	// No destination given: the folder is named after the shared folder itself.
	summary := runImport(t, tool,
		`{"action":"import_cloud","cloud_path":"https://cloud.mail.ru/public/dHhi/h2Sg1vzRe"}`)

	if cloud.publicHits != 1 {
		t.Errorf("публичная ссылка пошла не тем путём: %d", cloud.publicHits)
	}
	if !strings.Contains(strings.Join(drive.created, "|"), "Пирогово пристройка") {
		t.Errorf("папка объекта названа не по облачной: %v", drive.created)
	}
	if !strings.Contains(summary, "Загружено: 1") {
		t.Errorf("отчёт: %s", summary)
	}
}

func TestParsePublicLink(t *testing.T) {
	cases := map[string]string{
		"https://cloud.mail.ru/public/dHhi/h2Sg1vzRe":   "dHhi/h2Sg1vzRe",
		"http://cloud.mail.ru/public/dHhi/h2Sg1vzRe/":   "dHhi/h2Sg1vzRe",
		"https://cloud.mail.ru/public/dHhi/h2Sg1vzRe?x": "dHhi/h2Sg1vzRe",
		"dHhi/h2Sg1vzRe": "dHhi/h2Sg1vzRe",
	}
	for in, want := range cases {
		got, ok := builtin.ParsePublicLink(in)
		if !ok || got != want {
			t.Errorf("ParsePublicLink(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"/РУСКОН Раменское", "/НОГИНСК/СОЛОЩУК 11/ТП", ""} {
		if _, ok := builtin.ParsePublicLink(in); ok {
			t.Errorf("путь в облаке принят за ссылку: %q", in)
		}
	}
}
