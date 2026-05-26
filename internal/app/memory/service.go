package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Service struct {
	repo     output.MemoryRepository
	embedder output.EmbeddingProvider
}

func NewService(repo output.MemoryRepository, embedder output.EmbeddingProvider) *Service {
	return &Service{
		repo:     repo,
		embedder: embedder,
	}
}

func (s *Service) StoreFact(ctx context.Context, content, source string, tags []string) error {
	mem := entity.NewMemory(entity.MemoryFact, content, source, tags)

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		slog.Warn("failed to generate embedding, storing without", "error", err)
	} else {
		mem.Embedding = embedding
	}

	return s.repo.Store(ctx, mem)
}

func (s *Service) StoreSummary(ctx context.Context, content, source string, tags []string) error {
	mem := entity.NewMemory(entity.MemorySummary, content, source, tags)

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		slog.Warn("failed to generate embedding", "error", err)
	} else {
		mem.Embedding = embedding
	}

	return s.repo.Store(ctx, mem)
}

func (s *Service) StoreEvent(ctx context.Context, content, source string, tags []string) error {
	mem := entity.NewMemory(entity.MemoryEvent, content, source, tags)
	return s.repo.Store(ctx, mem)
}

func (s *Service) BuildContext(ctx context.Context, query string) (string, error) {
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("embed query: %w", err)
	}

	similar, err := s.repo.SearchSimilar(ctx, embedding, 5)
	if err != nil {
		return "", fmt.Errorf("search similar: %w", err)
	}

	facts, err := s.repo.GetByType(ctx, entity.MemoryFact, 10)
	if err != nil {
		slog.Warn("failed to get facts", "error", err)
	}

	summaries, err := s.repo.GetRecentSummaries(ctx, 7)
	if err != nil {
		slog.Warn("failed to get summaries", "error", err)
	}

	var b strings.Builder

	if len(facts) > 0 {
		b.WriteString("Known facts about the user:\n")
		for _, f := range facts {
			b.WriteString("- " + f.Content + "\n")
		}
		b.WriteString("\n")
	}

	if len(summaries) > 0 {
		b.WriteString("Recent conversation summaries:\n")
		for _, s := range summaries {
			b.WriteString("- " + s.Content + "\n")
		}
		b.WriteString("\n")
	}

	if len(similar) > 0 {
		b.WriteString("Related past context:\n")
		for _, s := range similar {
			b.WriteString("- " + s.Content + "\n")
		}
	}

	return b.String(), nil
}

func (s *Service) Search(ctx context.Context, query string, limit int) ([]*entity.Memory, error) {
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	return s.repo.SearchSimilar(ctx, embedding, limit)
}

func (s *Service) ListAll(ctx context.Context, limit, offset int) ([]*entity.Memory, error) {
	return s.repo.List(ctx, limit, offset)
}
