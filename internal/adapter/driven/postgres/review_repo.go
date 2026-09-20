package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
)

// ReviewRepo persists legal-review runs: the reviewed files with their hashes,
// the report and the models that produced it. This is the audit trail behind
// every conclusion handed to a client.
type ReviewRepo struct {
	db *DB
}

func NewReviewRepo(db *DB) *ReviewRepo {
	return &ReviewRepo{db: db}
}

// Save stores a run, minting its ID when the caller did not.
func (r *ReviewRepo) Save(ctx context.Context, run *legalreview.Run) (uuid.UUID, error) {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	files, err := json.Marshal(run.Files)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal review files: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO review_runs (id, folder, focus, files, report_md, pdf_path,
		    digest_model, coordinator_model, normativy_hash, started_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		run.ID, run.Folder, run.Focus, files, run.Report, run.PDFPath,
		run.Models.Digest, run.Models.Coordinator, run.NormativyHash,
		run.StartedAt, run.FinishedAt,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("save review run: %w", err)
	}
	return run.ID, nil
}

// ListRecent returns the latest runs, newest first.
func (r *ReviewRepo) ListRecent(ctx context.Context, limit int) ([]legalreview.Run, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, folder, focus, files, report_md, pdf_path,
		    digest_model, coordinator_model, normativy_hash, started_at, finished_at
		 FROM review_runs ORDER BY finished_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list review runs: %w", err)
	}
	defer rows.Close()

	var out []legalreview.Run
	for rows.Next() {
		var run legalreview.Run
		var files []byte
		if err := rows.Scan(&run.ID, &run.Folder, &run.Focus, &files, &run.Report, &run.PDFPath,
			&run.Models.Digest, &run.Models.Coordinator, &run.NormativyHash,
			&run.StartedAt, &run.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan review run: %w", err)
		}
		if len(files) > 0 {
			if err := json.Unmarshal(files, &run.Files); err != nil {
				return nil, fmt.Errorf("decode review files: %w", err)
			}
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// Compile-time: the repo satisfies the consumer's port.
var _ legalreview.RunStore = (*ReviewRepo)(nil)
