package builtin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/cron"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type fakeCronRepo struct {
	jobs []*entity.CronJob
}

func (f *fakeCronRepo) Save(_ context.Context, job *entity.CronJob) error {
	f.jobs = append(f.jobs, job)
	return nil
}

func (f *fakeCronRepo) Delete(_ context.Context, id uuid.UUID) error {
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeCronRepo) List(_ context.Context) ([]*entity.CronJob, error) {
	return f.jobs, nil
}

func (f *fakeCronRepo) GetDue(_ context.Context) ([]*entity.CronJob, error) { return nil, nil }

func (f *fakeCronRepo) MarkRun(_ context.Context, _ uuid.UUID, _ interface{}) error { return nil }

func newTestTool() (*ManageCron, *fakeCronRepo) {
	repo := &fakeCronRepo{}
	loc, _ := time.LoadLocation("Asia/Phnom_Penh")
	sched := cron.NewScheduler(repo, nil, nil, loc)
	return NewManageCron(sched, loc), repo
}

func TestManageCronAddListDelete(t *testing.T) {
	tool, repo := newTestTool()
	ctx := context.Background()

	// add
	out, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","schedule":"daily at 09:00","prompt":"check BTC price"}`))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var addResp map[string]any
	json.Unmarshal(out, &addResp)
	if addResp["status"] != "created" {
		t.Errorf("add status = %v, want created", addResp["status"])
	}
	if len(repo.jobs) != 1 {
		t.Fatalf("repo has %d jobs, want 1", len(repo.jobs))
	}
	if repo.jobs[0].Name != "check BTC price" {
		t.Errorf("derived name = %q", repo.jobs[0].Name)
	}

	// list
	out, err = tool.Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listResp struct {
		Count int `json:"count"`
		Jobs  []struct {
			Index int `json:"index"`
		} `json:"jobs"`
	}
	json.Unmarshal(out, &listResp)
	if listResp.Count != 1 || listResp.Jobs[0].Index != 1 {
		t.Errorf("list = %+v, want 1 job at index 1", listResp)
	}

	// delete
	if _, err = tool.Execute(ctx, json.RawMessage(`{"action":"delete","index":1}`)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(repo.jobs) != 0 {
		t.Errorf("repo has %d jobs after delete, want 0", len(repo.jobs))
	}
}

func TestManageCronAddValidation(t *testing.T) {
	tool, _ := newTestTool()
	ctx := context.Background()

	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","prompt":"x"}`)); err == nil {
		t.Error("expected error for missing schedule")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"add","schedule":"bogus","prompt":"x"}`)); err == nil {
		t.Error("expected error for bad schedule")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"frobnicate"}`)); err == nil {
		t.Error("expected error for unknown action")
	}
}
