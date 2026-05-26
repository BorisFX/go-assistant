package postgres

import (
	"context"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type ActivityRepo struct {
	db *DB
}

func NewActivityRepo(db *DB) *ActivityRepo {
	return &ActivityRepo{db: db}
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (r *ActivityRepo) Save(ctx context.Context, activity *entity.Activity) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO activities (id, type, name, input_tokens, output_tokens, cost_usd, duration_ms, session_id, conversation_id, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		activity.ID, string(activity.Type), activity.Name,
		activity.InputTokens, activity.OutputTokens, activity.CostUSD,
		activity.DurationMs, activity.SessionID, activity.ConversationID,
		nilIfEmpty(activity.Metadata), activity.CreatedAt,
	)
	return err
}

func (r *ActivityRepo) List(ctx context.Context, limit, offset int) ([]*entity.Activity, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, name, input_tokens, output_tokens, cost_usd, duration_ms, session_id, conversation_id, metadata, created_at
		 FROM activities ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []*entity.Activity
	for rows.Next() {
		var a entity.Activity
		if err := rows.Scan(&a.ID, &a.Type, &a.Name, &a.InputTokens, &a.OutputTokens, &a.CostUSD, &a.DurationMs, &a.SessionID, &a.ConversationID, &a.Metadata, &a.CreatedAt); err != nil {
			return nil, err
		}
		activities = append(activities, &a)
	}
	return activities, nil
}

func (r *ActivityRepo) GetCostSince(ctx context.Context, since time.Time) (float64, error) {
	var total float64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM activities WHERE created_at >= $1`,
		since,
	).Scan(&total)
	return total, err
}
