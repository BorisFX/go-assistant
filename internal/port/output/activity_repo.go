package output

import (
	"context"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type ActivityRepository interface {
	Save(ctx context.Context, activity *entity.Activity) error
	List(ctx context.Context, limit, offset int) ([]*entity.Activity, error)
	GetCostSince(ctx context.Context, since time.Time) (float64, error)
}
