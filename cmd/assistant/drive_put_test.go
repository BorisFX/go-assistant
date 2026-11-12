package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// fakeSink records folder and upload calls; "existing" seeds Drive contents
// per folder id so the skip logic can be exercised.
type fakeSink struct {
	folders  map[string]string // "parent/name" -> id
	existing map[string][]gworkspace.FileInfo
	uploads  []string // "parentID/name:size"
}

func newFakeSink() *fakeSink {
	return &fakeSink{folders: map[string]string{}, existing: map[string][]gworkspace.FileInfo{}}
}

func (f *fakeSink) EnsureFolder(_ context.Context, parentID, name string) (gworkspace.FileInfo, error) {
	key := parentID + "/" + name
	if id, ok := f.folders[key]; ok {
		return gworkspace.FileInfo{ID: id, Name: name, IsFolder: true}, nil
	}
	id := "id:" + key
	f.folders[key] = id
	return gworkspace.FileInfo{ID: id, Name: name, IsFolder: true}, nil
}

func (f *fakeSink) List(_ context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	return f.existing[folderID], nil
}

func (f *fakeSink) UploadStream(_ context.Context, parentID, name, _ string, r io.Reader) (gworkspace.FileInfo, error) {
	data, _ := io.ReadAll(r)
	f.uploads = append(f.uploads, parentID+"/"+name+":"+itoa(len(data)))
	return gworkspace.FileInfo{ID: "up:" + name, Name: name, Size: int64(len(data))}, nil
}

func itoa(n int) string { return string(rune('0' + n)) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPutDirMirrorsTreeAndSkipsUploaded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.pdf"), "aaa")
	writeFile(t, filepath.Join(dir, "same.pdf"), "same")
	writeFile(t, filepath.Join(dir, "sub", "b.zip"), "bb")
	writeFile(t, filepath.Join(dir, ".DS_Store"), "junk")

	sink := newFakeSink()
	// Destination folder already exists on Drive with same.pdf uploaded.
	sink.folders["/testHotel"] = "dest"
	sink.existing["dest"] = []gworkspace.FileInfo{{ID: "x", Name: "same.pdf", Size: 4}}

	st, err := putDir(context.Background(), sink, dir, "testHotel")
	if err != nil {
		t.Fatalf("putDir: %v", err)
	}
	if st.Uploaded != 2 || st.Skipped != 1 {
		t.Fatalf("stats = %+v, want 2 uploaded, 1 skipped", st)
	}
	want := map[string]bool{"dest/a.pdf:3": true, "id:dest/sub/b.zip:2": true}
	if len(sink.uploads) != 2 {
		t.Fatalf("uploads = %v", sink.uploads)
	}
	for _, u := range sink.uploads {
		if !want[u] {
			t.Errorf("unexpected upload %q, all: %v", u, sink.uploads)
		}
	}
}

func TestPutDirCreatesNestedDestination(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.pdf"), "a")

	sink := newFakeSink()
	if _, err := putDir(context.Background(), sink, dir, "Проекты/Отель"); err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.folders["/Проекты"]; !ok {
		t.Error("top-level segment not created")
	}
	if _, ok := sink.folders["id:/Проекты/Отель"]; !ok {
		t.Errorf("nested segment not created: %v", sink.folders)
	}
}

func TestPutDirRejectsMissingDir(t *testing.T) {
	if _, err := putDir(context.Background(), newFakeSink(), "/nonexistent/dir", "x"); err == nil {
		t.Fatal("expected error for missing directory")
	}
}
