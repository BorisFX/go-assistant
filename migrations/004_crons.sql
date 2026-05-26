CREATE TABLE IF NOT EXISTS cron_jobs (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    schedule TEXT NOT NULL,
    interval_seconds INT NOT NULL DEFAULT 0,
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cron_jobs_next_run ON cron_jobs(next_run_at) WHERE enabled = true;
