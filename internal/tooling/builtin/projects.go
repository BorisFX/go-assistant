package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
)

// ProjectService is the slice of the projects service this tool needs,
// declared here so the tool tests without Google or the spreadsheet.
type ProjectService interface {
	Routes(ctx context.Context) (map[string]projects.Route, error)
	Projects(ctx context.Context) ([]projects.Project, error)
	Register(ctx context.Context, p projects.Project) error
	Scaffold(ctx context.Context, project, routeCode string) ([]string, error)
	Status(ctx context.Context, project, routeCode string) (*projects.ProjectStatus, error)
}

// Projects exposes the project registry: which routes exist, where a project
// stands, and which documents its current stage is still missing.
type Projects struct {
	svc ProjectService
}

func NewProjects(svc ProjectService) *Projects { return &Projects{svc: svc} }

func (p *Projects) Name() string { return "projects" }

func (p *Projects) Description() string {
	return "Project registry: routes, project list, stage status with missing documents, folder scaffolding"
}

func (p *Projects) Category() string { return "projects" }

func (p *Projects) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["routes", "list", "status", "scaffold", "register"],
				"description": "routes — stage reference with expected documents; list — registered projects; status — where a project stands and what is missing; scaffold — create the route's stage folders; register — add a project row"
			},
			"project": {"type": "string", "description": "Project name, equal to its folder name on the drive"},
			"route": {"type": "string", "description": "Route code, e.g. ОНС_аренда. Optional for status if the project is already registered"},
			"address": {"type": "string", "description": "Object address, for register"},
			"owner": {"type": "string", "description": "Responsible person, for register"},
			"objects": {"type": "string", "description": "Number of physical objects, for register"},
			"amount": {"type": "string", "description": "Contract amount, for register"}
		},
		"required": ["action"]
	}`)
}

type projectsParams struct {
	Action  string `json:"action"`
	Project string `json:"project"`
	Route   string `json:"route"`
	Address string `json:"address"`
	Owner   string `json:"owner"`
	Objects string `json:"objects"`
	Amount  string `json:"amount"`
}

func (p *Projects) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var in projectsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	switch in.Action {
	case "routes":
		routes, err := p.svc.Routes(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(routes)

	case "list":
		list, err := p.svc.Projects(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"projects": list, "count": len(list)})

	case "status":
		st, err := p.svc.Status(ctx, in.Project, in.Route)
		if err != nil {
			return nil, err
		}
		return json.Marshal(st)

	case "scaffold":
		created, err := p.svc.Scaffold(ctx, in.Project, in.Route)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"project": in.Project, "folders": created})

	case "register":
		pr := projects.Project{
			Name: in.Project, Address: in.Address, Route: in.Route,
			Owner: in.Owner, Objects: in.Objects, Amount: in.Amount,
			Folder: in.Project,
		}
		if err := p.svc.Register(ctx, pr); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"registered": pr})

	default:
		return nil, fmt.Errorf("unknown action: %s", in.Action)
	}
}
