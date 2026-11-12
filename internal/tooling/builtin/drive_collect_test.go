package builtin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
)

// treeDrive serves a folder tree keyed by folder id, so recursion is exercised
// for real instead of being faked at the top level.
type treeDrive struct {
	tree       map[string][]gworkspace.FileInfo
	searchable []gworkspace.FileInfo // what Search returns, matched by substring
	downloads  int
	failOn     string
}

func (d *treeDrive) List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	return d.tree[folderID], nil
}
func (d *treeDrive) Search(ctx context.Context, text string) ([]gworkspace.FileInfo, error) {
	var out []gworkspace.FileInfo
	for _, f := range d.searchable {
		if strings.Contains(strings.ToLower(f.Name), strings.ToLower(text)) {
			out = append(out, f)
		}
	}
	return out, nil
}
func (d *treeDrive) SearchContent(ctx context.Context, text string) ([]gworkspace.FileInfo, error) {
	return d.Search(ctx, text)
}
func (d *treeDrive) Download(ctx context.Context, fileID string) ([]byte, error) {
	if fileID == d.failOn {
		return nil, errors.New("boom")
	}
	d.downloads++
	return []byte("content of " + fileID), nil
}
func (d *treeDrive) Upload(ctx context.Context, parentID, name, mimeType string, content []byte) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *treeDrive) EnsureFolder(ctx context.Context, parentID, name string) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *treeDrive) Move(ctx context.Context, fileID, newParentID string) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *treeDrive) CreateDoc(ctx context.Context, parentID, name, text string) (gworkspace.FileInfo, error) {
	return gworkspace.FileInfo{}, nil
}
func (d *treeDrive) ResolvePath(ctx context.Context, path string) (string, error) {
	if path == "Ленина_42/99_Переписка" || path == "Проекты/Ленина_42/99_Переписка" {
		return "corr", nil
	}
	return "", errors.New("folder not found")
}
func (d *treeDrive) EnsurePath(ctx context.Context, path string) (string, error) {
	return "corr", nil
}

func correspondenceTree() map[string][]gworkspace.FileInfo {
	return map[string][]gworkspace.FileInfo{
		"corr": {
			{ID: "f1", Name: "Уведомление.pdf"},
			{ID: "f2", Name: "подпись.sig"},
			{ID: "sub", Name: "2026-09-17_КП", IsFolder: true},
		},
		"sub": {
			{ID: "f3", Name: "Смета.xlsx"},
		},
	}
}

func TestDriveCollectFolderWalksSubfolders(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	dir := t.TempDir()
	tool := builtin.NewDriveFiles(drive, dir)

	paths, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка",
		[]string{".pdf", ".xlsx"}, 60)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 documents, got %d: %v", len(paths), paths)
	}
	// The .sig file is filtered out; the nested one is kept with its path.
	if !strings.HasSuffix(paths[1], filepath.Join("2026-09-17_КП", "Смета.xlsx")) {
		t.Errorf("nested path lost: %q", paths[1])
	}
	if data, err := os.ReadFile(paths[0]); err != nil || string(data) != "content of f1" {
		t.Errorf("file not cached locally: %v %q", err, data)
	}
}

func TestDriveCollectFolderReusesCache(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	dir := t.TempDir()
	tool := builtin.NewDriveFiles(drive, dir)
	exts := []string{".pdf", ".xlsx"}

	if _, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка", exts, 60); err != nil {
		t.Fatalf("first collect: %v", err)
	}
	if _, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка", exts, 60); err != nil {
		t.Fatalf("second collect: %v", err)
	}
	if drive.downloads != 2 {
		t.Errorf("expected 2 downloads across both runs, got %d", drive.downloads)
	}
}

func TestDriveCollectFolderGuardsFileCount(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	_, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка", []string{".pdf", ".xlsx"}, 1)
	if err == nil || !strings.Contains(err.Error(), "превышает лимит") {
		t.Fatalf("expected a limit error, got %v", err)
	}
	if drive.downloads != 0 {
		t.Errorf("guard must fire before any download, got %d", drive.downloads)
	}
}

func TestDriveCollectFolderSkipsFailedDownload(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree(), failOn: "f1"}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	paths, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка",
		[]string{".pdf", ".xlsx"}, 60)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("a failed download must not fail the batch: %v", paths)
	}
}

func TestDriveCollectFolderReportsEmpty(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	if _, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка", []string{".docx"}, 60); err == nil {
		t.Fatal("expected an error when nothing matches")
	}
}

func TestDriveCollectFolderReportsMissingFolder(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	if _, err := tool.CollectFolder(context.Background(), "Нет_такого", []string{".pdf"}, 60); err == nil {
		t.Fatal("expected an error for a missing folder")
	}
}

// A bare folder name («разбери папку testHotel») is looked up by name when it
// is not directly under the root, so the user need not remember «Проекты/».
func TestDriveCollectFolderFindsBareNameBySearch(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree(), searchable: []gworkspace.FileInfo{
		{ID: "f9", Name: "99_Переписка.pdf"}, // a file with the same stem must not win
		{ID: "corr", Name: "99_Переписка", IsFolder: true},
	}}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	paths, err := tool.CollectFolder(context.Background(), "99_переписка", []string{".pdf", ".xlsx"}, 60)
	if err != nil {
		t.Fatalf("CollectFolder: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("want 2 documents via search fallback, got %v", paths)
	}
}

func TestDriveCollectFolderAmbiguousNameIsAnError(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree(), searchable: []gworkspace.FileInfo{
		{ID: "corr", Name: "Переписка", IsFolder: true},
		{ID: "sub", Name: "Переписка", IsFolder: true},
	}}
	tool := builtin.NewDriveFiles(drive, t.TempDir())

	_, err := tool.CollectFolder(context.Background(), "Переписка", []string{".pdf"}, 60)
	if err == nil || !strings.Contains(err.Error(), "несколько") {
		t.Fatalf("want ambiguity error, got %v", err)
	}
}

func TestDriveCollectFolderTriesProjectsPrefix(t *testing.T) {
	drive := &treeDrive{tree: correspondenceTree()}
	tool := builtin.NewDriveFiles(drive, t.TempDir())
	tool.SetProjectsFolder("Проекты")

	paths, err := tool.CollectFolder(context.Background(), "Ленина_42/99_Переписка", []string{".pdf", ".xlsx"}, 60)
	if err != nil {
		t.Fatalf("CollectFolder: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("want 2 documents, got %v", paths)
	}
}
