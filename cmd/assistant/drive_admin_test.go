package main

import (
	"context"
	"strings"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

type fakeAdmin struct {
	tree  map[string][]gworkspace.FileInfo // parent id -> children
	moves []string                         // "id->parent"
}

func (f *fakeAdmin) Root() string { return "ROOT" }
func (f *fakeAdmin) List(_ context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	return f.tree[folderID], nil
}
func (f *fakeAdmin) EnsureFolder(_ context.Context, parentID, name string) (gworkspace.FileInfo, error) {
	for _, c := range f.tree[parentID] {
		if c.IsFolder && c.Name == name {
			return c, nil
		}
	}
	id := "new:" + parentID + "/" + name
	fi := gworkspace.FileInfo{ID: id, Name: name, IsFolder: true}
	f.tree[parentID] = append(f.tree[parentID], fi)
	return fi, nil
}
func (f *fakeAdmin) Move(_ context.Context, fileID, newParentID string) (gworkspace.FileInfo, error) {
	f.moves = append(f.moves, fileID+"->"+newParentID)
	return gworkspace.FileInfo{ID: fileID}, nil
}

func sampleTree() map[string][]gworkspace.FileInfo {
	return map[string][]gworkspace.FileInfo{
		"": {
			{ID: "p", Name: "Проекты", IsFolder: true},
			{ID: "stray", Name: "ГПЗУ.pdf", Size: 10},
		},
		"p": {
			{ID: "h", Name: "testHotel", IsFolder: true},
		},
		"h": {
			{ID: "f1", Name: "Заключение.pdf", Size: 5},
		},
	}
}

func TestRenderDriveTree(t *testing.T) {
	out := renderDriveTree(context.Background(), &fakeAdmin{tree: sampleTree()}, "", 5)
	for _, want := range []string{"Проекты/ [p]", "  testHotel/ [h]", "    Заключение.pdf [f1]", "ГПЗУ.pdf [stray]"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree lacks %q:\n%s", want, out)
		}
	}
}

func TestApplyDriveMovesResolvesPathsAndCreatesFolders(t *testing.T) {
	fa := &fakeAdmin{tree: sampleTree()}
	plan := "stray\tПроекты/testHotel/Исходные\n# comment\n\nf1\tПроекты/testHotel\nh\t/\n+\tПроекты/testHotel/99_Переписка\n"
	n, err := applyDriveMoves(context.Background(), fa, plan)
	if err != nil {
		t.Fatalf("applyDriveMoves: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 moves, got %d", n)
	}
	if fa.moves[0] != "stray->new:h/Исходные" || fa.moves[1] != "f1->h" || fa.moves[2] != "h->ROOT" {
		t.Fatalf("moves = %v", fa.moves)
	}
	if _, ok := fa.tree["new:h/99_Переписка"]; ok {
		t.Fatal("ensure-only line must not move anything")
	}
	found := false
	for _, c := range fa.tree["h"] {
		if c.Name == "99_Переписка" {
			found = true
		}
	}
	if !found {
		t.Fatal("ensure-only line must create the folder")
	}
}

func TestApplyDriveMovesRejectsBadLine(t *testing.T) {
	if _, err := applyDriveMoves(context.Background(), &fakeAdmin{tree: sampleTree()}, "no-tab-here\n"); err == nil {
		t.Fatal("malformed plan line must be an error before any move")
	}
}
