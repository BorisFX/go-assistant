package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type MemoryRepo struct {
	db *DB
}

func NewMemoryRepo(db *DB) *MemoryRepo {
	return &MemoryRepo{db: db}
}

func (r *MemoryRepo) Store(ctx context.Context, memory *entity.Memory) error {
	embStr := formatVector(memory.Embedding)

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO memories (id, type, content, tags, embedding, source, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8)`,
		memory.ID, string(memory.Type), memory.Content,
		pq.Array(memory.Tags), embStr, memory.Source,
		memory.CreatedAt, memory.ExpiresAt,
	)
	return err
}

func (r *MemoryRepo) GetByID(ctx context.Context, id uuid.UUID) (*entity.Memory, error) {
	var m entity.Memory
	var expiresAt sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at FROM memories WHERE id=$1`,
		id,
	).Scan(&m.ID, &m.Type, &m.Content, pq.Array(&m.Tags), &m.Source, &m.CreatedAt, &expiresAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if expiresAt.Valid {
		m.ExpiresAt = &expiresAt.Time
	}
	return &m, err
}

func (r *MemoryRepo) Update(ctx context.Context, memory *entity.Memory) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE memories SET content=$2, tags=$3, source=$4, expires_at=$5 WHERE id=$1`,
		memory.ID, memory.Content, pq.Array(memory.Tags), memory.Source, memory.ExpiresAt,
	)
	return err
}

func (r *MemoryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM memories WHERE id=$1`, id)
	return err
}

func (r *MemoryRepo) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]*entity.Memory, error) {
	embStr := formatVector(embedding)

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at
		 FROM memories
		 WHERE embedding IS NOT NULL
		 ORDER BY embedding <=> $1::vector
		 LIMIT $2`,
		embStr, limit,
	)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows)
}

func (r *MemoryRepo) GetByTags(ctx context.Context, tags []string, limit int) ([]*entity.Memory, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at
		 FROM memories
		 WHERE tags && $1
		 ORDER BY created_at DESC LIMIT $2`,
		pq.Array(tags), limit,
	)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows)
}

func (r *MemoryRepo) GetByType(ctx context.Context, memType entity.MemoryType, limit int) ([]*entity.Memory, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at
		 FROM memories WHERE type=$1 ORDER BY created_at DESC LIMIT $2`,
		string(memType), limit,
	)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows)
}

func (r *MemoryRepo) GetRecentSummaries(ctx context.Context, days int) ([]*entity.Memory, error) {
	since := time.Now().AddDate(0, 0, -days)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at
		 FROM memories WHERE type='summary' AND created_at >= $1
		 ORDER BY created_at DESC`,
		since,
	)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows)
}

func (r *MemoryRepo) List(ctx context.Context, limit, offset int) ([]*entity.Memory, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, content, tags, source, created_at, expires_at
		 FROM memories ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanMemories(rows)
}

func (r *MemoryRepo) Prune(ctx context.Context, olderThan time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM memories WHERE expires_at IS NOT NULL AND expires_at < $1`,
		olderThan,
	)
	return err
}

func scanMemories(rows *sql.Rows) ([]*entity.Memory, error) {
	defer rows.Close()
	var memories []*entity.Memory
	for rows.Next() {
		var m entity.Memory
		var expiresAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, pq.Array(&m.Tags), &m.Source, &m.CreatedAt, &expiresAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			m.ExpiresAt = &expiresAt.Time
		}
		memories = append(memories, &m)
	}
	return memories, rows.Err()
}

func formatVector(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%f", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
