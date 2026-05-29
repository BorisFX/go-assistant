package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type mockMemoryRepo struct {
	stored   []*entity.Memory
	searched []*entity.Memory
	scored   []output.ScoredMemory
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
func (m *mockMemoryRepo) SearchSimilarScored(ctx context.Context, embedding []float32, memType entity.MemoryType, limit int) ([]output.ScoredMemory, error) {
	return m.scored, nil
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
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45, DedupThreshold: 0.15, TopK: 5, FactLimit: 10, SummaryDays: 7,
	})

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
		scored: []output.ScoredMemory{
			{Memory: &entity.Memory{ID: uuid.New(), Type: entity.MemorySummary, Content: "discussed Go architecture"}, Distance: 0.1},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45, DedupThreshold: 0.15, TopK: 5, FactLimit: 10, SummaryDays: 7,
	})

	contextStr, err := svc.BuildContext(context.Background(), "tell me about Go patterns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contextStr == "" {
		t.Error("expected non-empty context")
	}
}

func TestMemoryService_StoreFact_SkipsDuplicate(t *testing.T) {
	repo := &mockMemoryRepo{
		scored: []output.ScoredMemory{
			{Memory: &entity.Memory{Type: entity.MemoryFact, Content: "user is a Go developer"}, Distance: 0.05},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45,
		DedupThreshold:      0.15,
		TopK:                5,
		FactLimit:           10,
		SummaryDays:         7,
	})

	if err := svc.StoreFact(context.Background(), "user is a Golang developer", "realtime", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.stored) != 0 {
		t.Fatalf("expected duplicate fact to be skipped, but %d were stored", len(repo.stored))
	}
}

func TestMemoryService_StoreFact_StoresWhenNotDuplicate(t *testing.T) {
	repo := &mockMemoryRepo{
		scored: []output.ScoredMemory{
			{Memory: &entity.Memory{Type: entity.MemoryFact, Content: "user lives in Phnom Penh"}, Distance: 0.6},
		},
	}
	svc := memory.NewService(repo, &mockEmbedder{}, memory.ServiceConfig{
		SimilarityThreshold: 0.45,
		DedupThreshold:      0.15,
		TopK:                5,
		FactLimit:           10,
		SummaryDays:         7,
	})

	if err := svc.StoreFact(context.Background(), "user is a Go developer", "realtime", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.stored) != 1 {
		t.Fatalf("expected 1 stored fact, got %d", len(repo.stored))
	}
}
