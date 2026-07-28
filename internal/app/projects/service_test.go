package projects_test

import (
	"context"
	"testing"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

type fakeSheets struct {
	ranges   map[string][][]string
	appended []string
}

func (f *fakeSheets) ReadRange(ctx context.Context, a1 string) ([][]string, error) {
	for k, v := range f.ranges {
		if len(a1) >= len(k) && a1[:len(k)] == k {
			return v, nil
		}
	}
	return nil, nil
}

func (f *fakeSheets) AppendRow(ctx context.Context, sheet string, row []string) error {
	f.appended = row
	return nil
}

type fakeDriveSvc struct {
	folders map[string][]string // path -> file names
	ensured []string
}

func (f *fakeDriveSvc) ResolvePath(ctx context.Context, path string) (string, error) {
	if _, ok := f.folders[path]; !ok {
		return "", context.Canceled // any error: folder absent
	}
	return path, nil
}

func (f *fakeDriveSvc) EnsurePath(ctx context.Context, path string) (string, error) {
	f.ensured = append(f.ensured, path)
	return path, nil
}

func (f *fakeDriveSvc) List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error) {
	var out []gworkspace.FileInfo
	for _, n := range f.folders[folderID] {
		out = append(out, gworkspace.FileInfo{Name: n})
	}
	return out, nil
}

func newSvc(folders map[string][]string) (*projects.Service, *fakeSheets, *fakeDriveSvc) {
	sh := &fakeSheets{ranges: map[string][][]string{
		"Маршруты": {
			{"Маршрут", "№", "Этап", "Ожидаемые документы", "Госорганы", "Дней"},
			{"ОНС_аренда", "1", "Подготовка", "Дорожная карта", "", "14"},
			{"ОНС_аренда", "2", "Техпланы", "Технические планы; XML-файлы", "", "30"},
		},
		"Проекты": {
			{"Проект", "Адрес", "Маршрут"},
			{"Vertex", "Мытищи", "ОНС_аренда"},
		},
	}}
	dr := &fakeDriveSvc{folders: folders}
	return projects.NewService(sh, dr), sh, dr
}

func TestStatusFindsRouteFromRegistry(t *testing.T) {
	svc, _, _ := newSvc(map[string][]string{
		"Vertex":               {},
		"Vertex/01_Подготовка": {"Дорожная карта.pdf"},
		"Vertex/02_Техпланы":   {},
	})

	st, err := svc.Status(context.Background(), "Vertex", "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Route != "ОНС_аренда" {
		t.Errorf("route must come from the registry, got %q", st.Route)
	}
	if st.Current.Num != 2 {
		t.Errorf("current stage: got %d", st.Current.Num)
	}
	if len(st.Current.Missing) != 2 {
		t.Errorf("missing on current stage: got %v", st.Current.Missing)
	}
}

// A stage nobody has started has no folder yet; that is normal, not an error.
func TestStatusTreatsMissingFolderAsEmptyStage(t *testing.T) {
	svc, _, _ := newSvc(map[string][]string{"Vertex": {}})

	st, err := svc.Status(context.Background(), "Vertex", "ОНС_аренда")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(st.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(st.Stages))
	}
	if st.Current.Num != 1 {
		t.Errorf("current stage: got %d", st.Current.Num)
	}
}

func TestStatusUnknownProjectWithoutRoute(t *testing.T) {
	svc, _, _ := newSvc(map[string][]string{})

	if _, err := svc.Status(context.Background(), "НетТакого", ""); err == nil {
		t.Fatal("expected an error naming the missing project")
	}
}

func TestScaffoldCreatesEveryRouteFolder(t *testing.T) {
	svc, _, dr := newSvc(map[string][]string{"Vertex": {}})

	created, err := svc.Scaffold(context.Background(), "Vertex", "ОНС_аренда")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if len(created) != 4 {
		t.Fatalf("expected source + 2 stages + mail, got %v", created)
	}
	if created[0] != projects.SourceFolder {
		t.Errorf("first folder: got %q", created[0])
	}
	if len(dr.ensured) == 0 {
		t.Error("scaffold must ensure paths on the drive")
	}
}

func TestScaffoldUnknownRouteListsAvailable(t *testing.T) {
	svc, _, _ := newSvc(map[string][]string{"Vertex": {}})

	_, err := svc.Scaffold(context.Background(), "Vertex", "Опечатка")
	if err == nil {
		t.Fatal("expected an error for an unknown route")
	}
}

func TestRegisterWritesRow(t *testing.T) {
	svc, sh, _ := newSvc(map[string][]string{})

	err := svc.Register(context.Background(), projects.Project{Name: "Мира_7", Route: "ОНС_аренда", Address: "Мира 7"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if sh.appended[0] != "Мира_7" || sh.appended[2] != "ОНС_аренда" {
		t.Errorf("appended row: %v", sh.appended)
	}
}

func TestRegisterRequiresNameAndRoute(t *testing.T) {
	svc, _, _ := newSvc(map[string][]string{})

	if err := svc.Register(context.Background(), projects.Project{Name: "X"}); err == nil {
		t.Fatal("expected an error when the route is missing")
	}
}
