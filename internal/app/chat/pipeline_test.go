package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type stubTool struct {
	name   string
	result string
	err    error
}

func (t *stubTool) Name() string                { return t.name }
func (t *stubTool) Description() string         { return "stub" }
func (t *stubTool) Category() string            { return "test" }
func (t *stubTool) Schema() json.RawMessage     { return json.RawMessage(`{"type":"object"}`) }
func (t *stubTool) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if t.err != nil {
		return nil, t.err
	}
	return json.RawMessage(t.result), nil
}

type stubRegistry struct{ tools map[string]output.Tool }

func (r *stubRegistry) Register(t output.Tool) error { r.tools[t.Name()] = t; return nil }
func (r *stubRegistry) ListTools() []entity.ToolSummary {
	return nil
}
func (r *stubRegistry) LoadSchema(name string) (*entity.ToolDefinition, error) { return nil, nil }
func (r *stubRegistry) LoadSchemas(names []string) ([]entity.ToolDefinition, error) {
	return nil, nil
}
func (r *stubRegistry) GetTool(name string) (output.Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return t, nil
}

type recordingActivityRepo struct {
	mu   sync.Mutex
	seen []*entity.Activity
}

func (r *recordingActivityRepo) Save(ctx context.Context, a *entity.Activity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, a)
	return nil
}
func (r *recordingActivityRepo) List(ctx context.Context, limit, offset int) ([]*entity.Activity, error) {
	return nil, nil
}
func (r *recordingActivityRepo) GetCostSince(ctx context.Context, since time.Time) (float64, error) {
	return 0, nil
}

// stubLLM ends the loop by replying without tool calls.
type stubLLM struct{}

func (l *stubLLM) Chat(ctx context.Context, req output.LLMRequest) (*output.LLMResponse, error) {
	return &output.LLMResponse{Content: "готово"}, nil
}
func (l *stubLLM) ChatStream(ctx context.Context, req output.LLMRequest, onChunk func(string)) (*output.LLMResponse, error) {
	return l.Chat(ctx, req)
}

func runLoop(t *testing.T, tool output.Tool, repo output.ActivityRepository) {
	t.Helper()
	reg := &stubRegistry{tools: map[string]output.Tool{}}
	if tool != nil {
		reg.Register(tool)
	}
	loop := chat.NewToolLoop(reg, 4, 10000).WithActivityRepo(repo)

	initial := &output.LLMResponse{
		ToolCalls: []entity.ToolCall{{ID: "c1", Name: "drive_files", Args: `{"action":"list","path":""}`}},
	}
	_, err := loop.Run(context.Background(), &stubLLM{}, nil, initial, nil, 0.2, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

// Without this record there is no way to tell whether the model actually used a
// tool or merely claimed to — the failure mode this test exists to prevent.
func TestToolLoopRecordsSuccessfulCall(t *testing.T) {
	repo := &recordingActivityRepo{}
	runLoop(t, &stubTool{name: "drive_files", result: `{"files":[]}`}, repo)

	if len(repo.seen) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(repo.seen))
	}
	a := repo.seen[0]
	if a.Type != entity.ActivityToolCall {
		t.Errorf("type: got %q", a.Type)
	}
	if a.Name != "drive_files" {
		t.Errorf("name: got %q", a.Name)
	}
	if !strings.Contains(a.Metadata, `"action":"list"`) {
		t.Errorf("metadata must carry the call args, got %q", a.Metadata)
	}
	if !strings.Contains(a.Metadata, `"ok":true`) {
		t.Errorf("metadata must mark success, got %q", a.Metadata)
	}
}

func TestToolLoopRecordsFailedCall(t *testing.T) {
	repo := &recordingActivityRepo{}
	runLoop(t, &stubTool{name: "drive_files", err: errors.New("folder not found")}, repo)

	if len(repo.seen) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(repo.seen))
	}
	if !strings.Contains(repo.seen[0].Metadata, "folder not found") {
		t.Errorf("metadata must carry the error, got %q", repo.seen[0].Metadata)
	}
}

// A name the instance does not have is still worth recording: it shows the model
// tried, which is the difference between a bad prompt and a missing tool.
func TestToolLoopRecordsUnknownTool(t *testing.T) {
	repo := &recordingActivityRepo{}
	runLoop(t, nil, repo)

	if len(repo.seen) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(repo.seen))
	}
	if !strings.Contains(repo.seen[0].Metadata, "not registered") {
		t.Errorf("metadata must say the tool was missing, got %q", repo.seen[0].Metadata)
	}
}

func TestToolLoopWithoutRepoDoesNotPanic(t *testing.T) {
	runLoop(t, &stubTool{name: "drive_files", result: `{}`}, nil)
}
