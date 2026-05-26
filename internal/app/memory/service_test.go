package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type mockMemoryRepo struct {
	stored   []*entity.Memory
	searched []*entity.Memory
}

func (m *mockMemoryRepo) Store(ctx context.Context, mem *entity.Memory) error {
	m.stored = append(m.stored, mem)
	return nil
}
func (m *mockMemoryRepo) GetByID(ctx context.Context, id uuid.UUID) (*entity.Memory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) Update(ctx context.Context, mem *entity.Memory) error { return nil }
func (m *mockMemoryRepo) Delete(ctx context.Context, id uuid.UUID) error       { return nil }
func (m *mockMemoryRepo) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]*entity.Memory, error) {
	return m.searched, nil
}
func (m *mockMemoryRepo) GetByTags(ctx context.Context, tags []string, limit int) ([]*entity.Memory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) GetByType(ctx context.Context, memType entity.MemoryType, limit int) ([]*entity.Memory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) GetRecentSummaries(ctx context.Context, days int) ([]*entity.Memory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) List(ctx context.Context, limit, offset int) ([]*entity.Memory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) Prune(ctx context.Context, olderThan time.Time) error { return nil }

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 1536), nil
}

func TestMemoryService_StoreFact(t *testing.T) {
	repo := &mockMemoryRepo{}
	svc := memory.NewService(repo, &mockEmbedder{})

	err := svc.StoreFact(context.Background(), "user is a Go developer", "telegram", []string{"user", "skills"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.stored) != 1 {
		t.Fatalf("expected 1 stored memory, got %d", len(repo.stored))
	}

	if repo.stored[0].Type != entity.MemoryFact {
		t.Errorf("expected type fact, got %s", repo.stored[0].Type)
	}

	if len(repo.stored[0].Embedding) != 1536 {
		t.Errorf("expected embedding with 1536 dims, got %d", len(repo.stored[0].Embedding))
	}
}

func TestMemoryService_BuildContext(t *testing.T) {
	repo := &mockMemoryRepo{
		searched: []*entity.Memory{
			{ID: uuid.New(), Type: entity.MemorySummary, Content: "discussed Go architecture"},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{})

	contextStr, err := svc.BuildContext(context.Background(), "tell me about Go patterns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contextStr == "" {
		t.Error("expected non-empty context")
	}
}
