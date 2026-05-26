package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Executor struct {
	registry     output.ToolRegistry
	activityRepo output.ActivityRepository
}

func NewExecutor(registry output.ToolRegistry, activityRepo output.ActivityRepository) *Executor {
	return &Executor{
		registry:     registry,
		activityRepo: activityRepo,
	}
}

type ExecutionResult struct {
	ToolName   string
	Result     json.RawMessage
	DurationMs int64
	Error      error
}

func (e *Executor) Execute(ctx context.Context, call entity.ToolCall, sessionID string, convID uuid.UUID) (*ExecutionResult, error) {
	tool, err := e.registry.GetTool(call.Name)
	if err != nil {
		return nil, fmt.Errorf("get tool %q: %w", call.Name, err)
	}

	start := time.Now()
	result, execErr := tool.Execute(ctx, json.RawMessage(call.Args))
	duration := time.Since(start).Milliseconds()

	slog.Info("tool executed",
		"tool", call.Name,
		"duration_ms", duration,
		"error", execErr,
	)

	if e.activityRepo != nil {
		activity := entity.NewActivity(entity.ActivityToolCall, call.Name, sessionID, convID)
		activity.DurationMs = duration
		if saveErr := e.activityRepo.Save(ctx, activity); saveErr != nil {
			slog.Error("failed to save activity", "error", saveErr)
		}
	}

	return &ExecutionResult{
		ToolName:   call.Name,
		Result:     result,
		DurationMs: duration,
		Error:      execErr,
	}, nil
}
