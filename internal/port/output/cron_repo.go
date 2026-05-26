package output

import (
	"context"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type CronRepository interface {
	Save(ctx context.Context, job *entity.CronJob) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]*entity.CronJob, error)
	GetDue(ctx context.Context) ([]*entity.CronJob, error)
	MarkRun(ctx context.Context, id uuid.UUID, nextRun interface{}) error
}
