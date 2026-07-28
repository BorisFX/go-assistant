package projects

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// Narrow interfaces declared here, not imported, so the package is testable
// with fakes and never reaches Google in a test.
type sheetClient interface {
	ReadRange(ctx context.Context, a1 string) ([][]string, error)
	AppendRow(ctx context.Context, sheet string, row []string) error
}

type driveClient interface {
	ResolvePath(ctx context.Context, path string) (string, error)
	EnsurePath(ctx context.Context, path string) (string, error)
	List(ctx context.Context, folderID string) ([]gworkspace.FileInfo, error)
}

const (
	routesRange   = "Маршруты!A1:F200"
	projectsRange = "Проекты!A1:K200"
	projectsSheet = "Проекты"
)

type Service struct {
	sheets sheetClient
	drive  driveClient
}

func NewService(sheets sheetClient, drive driveClient) *Service {
	return &Service{sheets: sheets, drive: drive}
}

// Project is one row of the Проекты sheet.
type Project struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	Route   string `json:"route"`
	Stage   string `json:"stage,omitempty"`
	Due     string `json:"due,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Folder  string `json:"folder,omitempty"`
	Objects string `json:"objects,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Paid    string `json:"paid,omitempty"`
	Note    string `json:"note,omitempty"`
}

func (s *Service) Routes(ctx context.Context) (map[string]Route, error) {
	rows, err := s.sheets.ReadRange(ctx, routesRange)
	if err != nil {
		return nil, fmt.Errorf("read routes: %w", err)
	}
	routes := ParseRoutes(rows)
	if len(routes) == 0 {
		return nil, fmt.Errorf("лист «Маршруты» пуст — заполните справочник маршрутов")
	}
	for code, r := range routes {
		sort.Slice(r.Stages, func(i, j int) bool { return r.Stages[i].Num < r.Stages[j].Num })
		routes[code] = r
	}
	return routes, nil
}

func (s *Service) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.sheets.ReadRange(ctx, projectsRange)
	if err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}

	var out []Project
	for i, row := range rows {
		if i == 0 || cell(row, 0) == "" {
			continue
		}
		out = append(out, Project{
			Name: cell(row, 0), Address: cell(row, 1), Route: cell(row, 2),
			Stage: cell(row, 3), Due: cell(row, 4), Owner: cell(row, 5),
			Folder: cell(row, 6), Objects: cell(row, 7), Amount: cell(row, 8),
			Paid: cell(row, 9), Note: cell(row, 10),
		})
	}
	return out, nil
}

func (s *Service) Register(ctx context.Context, p Project) error {
	if p.Name == "" || p.Route == "" {
		return fmt.Errorf("register: имя проекта и маршрут обязательны")
	}
	return s.sheets.AppendRow(ctx, projectsSheet, []string{
		p.Name, p.Address, p.Route, p.Stage, p.Due, p.Owner,
		p.Folder, p.Objects, p.Amount, p.Paid, p.Note,
	})
}

// Scaffold creates the folder for every stage of the route. Idempotent: an
// existing folder is reused, so it is safe to re-run after a partial failure.
func (s *Service) Scaffold(ctx context.Context, project, routeCode string) ([]string, error) {
	routes, err := s.Routes(ctx)
	if err != nil {
		return nil, err
	}
	route, ok := routes[routeCode]
	if !ok {
		return nil, fmt.Errorf("маршрут %q не найден; доступны: %s", routeCode, strings.Join(routeCodes(routes), ", "))
	}

	if _, err := s.drive.EnsurePath(ctx, project); err != nil {
		// The project folder itself is never auto-created — see EnsurePath.
		return nil, fmt.Errorf("папка проекта %q не найдена, создайте её сначала: %w", project, err)
	}

	created := make([]string, 0, len(route.Folders()))
	for _, folder := range route.Folders() {
		if _, err := s.drive.EnsurePath(ctx, project+"/"+folder); err != nil {
			return created, fmt.Errorf("создать %s/%s: %w", project, folder, err)
		}
		created = append(created, folder)
	}
	return created, nil
}

func routeCodes(routes map[string]Route) []string {
	out := make([]string, 0, len(routes))
	for c := range routes {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ProjectStatus answers "where is this project and what is missing".
type ProjectStatus struct {
	Project string        `json:"project"`
	Route   string        `json:"route"`
	Current StageStatus   `json:"current"`
	Stages  []StageStatus `json:"stages"`
	Source  []string      `json:"source_documents"`
}

// Status walks the route's stages, listing each folder and comparing it with
// the documents the stage is expected to produce. The route may be given
// explicitly for a project that is not in the registry yet.
func (s *Service) Status(ctx context.Context, project, routeCode string) (*ProjectStatus, error) {
	if project == "" {
		return nil, fmt.Errorf("status: имя проекта обязательно")
	}

	if routeCode == "" {
		known, err := s.Projects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range known {
			if strings.EqualFold(p.Name, project) {
				routeCode = p.Route
				break
			}
		}
		if routeCode == "" {
			return nil, fmt.Errorf("проект %q не найден в реестре — укажите маршрут явно или заведите проект", project)
		}
	}

	routes, err := s.Routes(ctx)
	if err != nil {
		return nil, err
	}
	route, ok := routes[routeCode]
	if !ok {
		return nil, fmt.Errorf("маршрут %q не найден; доступны: %s", routeCode, strings.Join(routeCodes(routes), ", "))
	}

	out := &ProjectStatus{Project: project, Route: routeCode}
	out.Source = s.fileNames(ctx, project+"/"+SourceFolder)

	for _, stage := range route.Stages {
		files := s.fileNames(ctx, project+"/"+stage.Folder())
		out.Stages = append(out.Stages, CheckStage(stage, files))
	}
	out.Current = CurrentStage(out.Stages)
	return out, nil
}

// fileNames returns the names in a folder. A missing folder yields no names
// rather than an error: a stage nobody has started yet is a normal state, and
// failing here would make the whole report unavailable.
func (s *Service) fileNames(ctx context.Context, path string) []string {
	id, err := s.drive.ResolvePath(ctx, path)
	if err != nil {
		return nil
	}
	files, err := s.drive.List(ctx, id)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		if !f.IsFolder {
			names = append(names, f.Name)
		}
	}
	return names
}
