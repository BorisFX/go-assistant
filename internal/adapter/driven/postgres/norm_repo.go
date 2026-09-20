package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/app/norms"
)

// NormRepo stores the normative corpus: documents and their unit chunks with
// embeddings, in the same pgvector setup the memory store uses.
type NormRepo struct {
	db *DB
}

func NewNormRepo(db *DB) *NormRepo { return &NormRepo{db: db} }

func (r *NormRepo) UpsertDocument(ctx context.Context, d norms.Document) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO norm_documents (code, title, edition, source, file_hash, ingested_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (code) DO UPDATE SET
		   title = EXCLUDED.title, edition = EXCLUDED.edition, source = EXCLUDED.source,
		   file_hash = EXCLUDED.file_hash, ingested_at = EXCLUDED.ingested_at`,
		d.Code, d.Title, d.Edition, d.Source, d.FileHash, d.IngestedAt)
	return err
}

// ReplaceChunks swaps a document's chunks atomically, so a reader never sees
// half of the old edition and half of the new one.
func (r *NormRepo) ReplaceChunks(ctx context.Context, docCode string, chunks []norms.Chunk) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM norm_chunks WHERE doc_code = $1`, docCode); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO norm_chunks (doc_code, ref, ref_norm, ord, text, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6::vector)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chunks {
		var emb any
		if len(c.Embedding) > 0 {
			emb = formatVector(c.Embedding)
		}
		if _, err := stmt.ExecContext(ctx, docCode, c.Ref, norms.NormalizeRef(c.Ref), c.Ord, c.Text, emb); err != nil {
			return fmt.Errorf("insert chunk %s/%s: %w", docCode, c.Ref, err)
		}
	}
	return tx.Commit()
}

func (r *NormRepo) FindByRef(ctx context.Context, docCode, refPrefix string, limit int) ([]norms.Chunk, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, doc_code, ref, ord, text FROM norm_chunks
		 WHERE doc_code = $1 AND ($2 = '' OR ref_norm = $2 OR ref_norm LIKE $2 || ' %')
		 ORDER BY ord LIMIT $3`,
		docCode, refPrefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (r *NormRepo) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]norms.Chunk, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, doc_code, ref, ord, text FROM norm_chunks
		 WHERE embedding IS NOT NULL
		 ORDER BY embedding <=> $1::vector LIMIT $2`,
		formatVector(embedding), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (r *NormRepo) ListDocuments(ctx context.Context) ([]norms.Document, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT code, title, edition, source, file_hash, ingested_at FROM norm_documents ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []norms.Document
	for rows.Next() {
		var d norms.Document
		if err := rows.Scan(&d.Code, &d.Title, &d.Edition, &d.Source, &d.FileHash, &d.IngestedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *NormRepo) DocumentHash(ctx context.Context, code string) (string, error) {
	var hash string
	err := r.db.QueryRowContext(ctx, `SELECT file_hash FROM norm_documents WHERE code = $1`, code).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func scanChunks(rows *sql.Rows) ([]norms.Chunk, error) {
	var out []norms.Chunk
	for rows.Next() {
		var c norms.Chunk
		if err := rows.Scan(&c.ID, &c.DocCode, &c.Ref, &c.Ord, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Compile-time: the repo satisfies the corpus port.
var _ norms.Repository = (*NormRepo)(nil)
