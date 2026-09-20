CREATE TABLE IF NOT EXISTS review_runs (
    id UUID PRIMARY KEY,
    folder TEXT NOT NULL,
    focus TEXT NOT NULL DEFAULT '',
    files JSONB NOT NULL DEFAULT '[]',
    report_md TEXT NOT NULL,
    pdf_path TEXT NOT NULL DEFAULT '',
    digest_model TEXT NOT NULL DEFAULT '',
    coordinator_model TEXT NOT NULL DEFAULT '',
    normativy_hash TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_review_runs_finished_at ON review_runs(finished_at DESC);
CREATE INDEX idx_review_runs_folder ON review_runs(folder);
