package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/cron"
)

// ManageCron lets the assistant create, list, and delete its own scheduled
// tasks in a single tool call. Jobs are stored per-instance, so each bot
// version only ever sees and fires its own crons.
type ManageCron struct {
	sched *cron.Scheduler
	loc   *time.Location
}

func NewManageCron(sched *cron.Scheduler, loc *time.Location) *ManageCron {
	if loc == nil {
		loc = time.UTC
	}
	return &ManageCron{sched: sched, loc: loc}
}

func (m *ManageCron) Name() string     { return "manage_cron" }
func (m *ManageCron) Category() string { return "cron" }

func (m *ManageCron) Description() string {
	return "Create, list, or delete scheduled tasks (cron jobs). When a job fires, " +
		"the bot runs its prompt and sends the owner the result. Create a job in one " +
		"call with action=add, a schedule, and a prompt. Schedule formats: intervals " +
		"(\"every 30m\", \"every 2h\", \"hourly\", \"daily\") or clock times in the owner's " +
		"timezone (\"daily at 09:00\", \"every day at 18:30\", \"at 07:00\")."
}

func (m *ManageCron) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["add", "list", "delete"],
				"description": "What to do"
			},
			"schedule": {
				"type": "string",
				"description": "For add: e.g. \"every 30m\", \"every 2h\", \"daily\", \"daily at 09:00\", \"at 18:30\""
			},
			"prompt": {
				"type": "string",
				"description": "For add: the task the bot performs when the job fires"
			},
			"name": {
				"type": "string",
				"description": "For add: optional short label for the job"
			},
			"index": {
				"type": "integer",
				"description": "For delete: the 1-based number from the list"
			}
		},
		"required": ["action"]
	}`)
}

type manageCronParams struct {
	Action   string `json:"action"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Name     string `json:"name"`
	Index    int    `json:"index"`
}

func (m *ManageCron) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p manageCronParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "add":
		return m.add(ctx, p)
	case "list":
		return m.list(ctx)
	case "delete", "del":
		return m.delete(ctx, p)
	default:
		return nil, fmt.Errorf("unknown action %q, use add/list/delete", p.Action)
	}
}

func (m *ManageCron) add(ctx context.Context, p manageCronParams) (json.RawMessage, error) {
	if strings.TrimSpace(p.Schedule) == "" || strings.TrimSpace(p.Prompt) == "" {
		return nil, fmt.Errorf("add requires both schedule and prompt")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = deriveName(p.Prompt)
	}

	job, err := m.sched.Add(ctx, name, p.Prompt, p.Schedule)
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"status":   "created",
		"name":     job.Name,
		"schedule": job.Schedule,
		"next_run": job.NextRunAt.In(m.loc).Format("2006-01-02 15:04 MST"),
	})
}

func (m *ManageCron) list(ctx context.Context) (json.RawMessage, error) {
	jobs, err := m.sched.List(ctx)
	if err != nil {
		return nil, err
	}
	type item struct {
		Index    int    `json:"index"`
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		NextRun  string `json:"next_run"`
	}
	items := make([]item, 0, len(jobs))
	for i, j := range jobs {
		items = append(items, item{
			Index:    i + 1,
			Name:     j.Name,
			Schedule: j.Schedule,
			Prompt:   j.Prompt,
			NextRun:  j.NextRunAt.In(m.loc).Format("2006-01-02 15:04 MST"),
		})
	}
	return json.Marshal(map[string]any{"jobs": items, "count": len(items)})
}

func (m *ManageCron) delete(ctx context.Context, p manageCronParams) (json.RawMessage, error) {
	if p.Index < 1 {
		return nil, fmt.Errorf("delete requires a positive index from the list")
	}
	if err := m.sched.Delete(ctx, p.Index); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"status": "deleted", "index": p.Index})
}

func deriveName(prompt string) string {
	prompt = strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
	const max = 40
	if len(prompt) <= max {
		return prompt
	}
	return strings.TrimSpace(prompt[:max])
}
