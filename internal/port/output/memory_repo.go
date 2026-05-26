package output

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type MemoryRepository interface {
	Store(ctx context.Context, memory *entity.Memory) error
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Memory, error)
	Update(ctx context.Context, memory *entity.Memory) error
	Delete(ctx context.Context, id uuid.UUID) error
	SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]*entity.Memory, error)
	GetByTags(ctx context.Context, tags []string, limit int) ([]*entity.Memory, error)
	GetByType(ctx context.Context, memType entity.MemoryType, limit int) ([]*entity.Memory, error)
	GetRecentSummaries(ctx context.Context, days int) ([]*entity.Memory, error)
	List(ctx context.Context, limit, offset int) ([]*entity.Memory, error)
	Prune(ctx context.Context, olderThan time.Time) error
}
