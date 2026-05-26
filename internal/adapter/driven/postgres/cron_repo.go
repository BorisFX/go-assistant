package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
)

type CronRepo struct {
	db *DB
}

func NewCronRepo(db *DB) *CronRepo {
	return &CronRepo{db: db}
}

func (r *CronRepo) Save(ctx context.Context, job *entity.CronJob) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cron_jobs (id, name, prompt, schedule, interval_seconds, next_run_at, last_run_at, enabled, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (id) DO UPDATE SET name=$2, prompt=$3, schedule=$4, interval_seconds=$5, next_run_at=$6, enabled=$8`,
		job.ID, job.Name, job.Prompt, job.Schedule, job.IntervalSeconds, job.NextRunAt, job.LastRunAt, job.Enabled, job.CreatedAt,
	)
	return err
}

func (r *CronRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id=$1`, id)
	return err
}

func (r *CronRepo) List(ctx context.Context) ([]*entity.CronJob, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, prompt, schedule, interval_seconds, next_run_at, last_run_at, enabled, created_at
		 FROM cron_jobs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*entity.CronJob
	for rows.Next() {
		var j entity.CronJob
		var lastRun sql.NullTime
		if err := rows.Scan(&j.ID, &j.Name, &j.Prompt, &j.Schedule, &j.IntervalSeconds, &j.NextRunAt, &lastRun, &j.Enabled, &j.CreatedAt); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			j.LastRunAt = &lastRun.Time
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func (r *CronRepo) GetDue(ctx context.Context) ([]*entity.CronJob, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, prompt, schedule, interval_seconds, next_run_at, last_run_at, enabled, created_at
		 FROM cron_jobs WHERE enabled = true AND next_run_at <= $1`,
		time.Now(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*entity.CronJob
	for rows.Next() {
		var j entity.CronJob
		var lastRun sql.NullTime
		if err := rows.Scan(&j.ID, &j.Name, &j.Prompt, &j.Schedule, &j.IntervalSeconds, &j.NextRunAt, &lastRun, &j.Enabled, &j.CreatedAt); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			j.LastRunAt = &lastRun.Time
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func (r *CronRepo) MarkRun(ctx context.Context, id uuid.UUID, nextRun interface{}) error {
	nextRunTime, ok := nextRun.(time.Time)
	if !ok {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE cron_jobs SET last_run_at = NOW(), next_run_at = $2 WHERE id = $1`,
		id, nextRunTime,
	)
	return err
}
