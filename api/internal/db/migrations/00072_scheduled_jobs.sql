-- +goose Up
-- +goose StatementBegin
-- Scheduled jobs (plan: docs/plans/scheduled-jobs.md). User-facing cron: run a
-- command on a schedule inside one of an app's running service containers. The
-- shape mirrors backup_configs/backup_runs — same scheduler pattern, same run
-- lifecycle — minus the destination (output is kept as a bounded tail on the run
-- row instead of shipped anywhere). Jobs are a core feature, not gated by
-- VAC_MANAGED_SERVICES; the scheduler goroutine still only starts when ≥1
-- enabled job exists, so idle footprint stays zero.
CREATE TABLE scheduled_jobs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id           UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    service_name     TEXT NOT NULL,
    command          TEXT NOT NULL,
    frequency        TEXT NOT NULL,                 -- interval | daily | weekly
    interval_minutes INT,                           -- when frequency='interval'
    hour_of_day      INT  NOT NULL DEFAULT 3,
    day_of_week      INT,                           -- 0-6 (Sun=0), NULL unless weekly
    timeout_seconds  INT  NOT NULL DEFAULT 1800,    -- per-run hard cap (default 30 min)
    enabled          BOOL NOT NULL DEFAULT TRUE,
    last_run         TIMESTAMPTZ,                   -- denormalized for the UI
    next_run         TIMESTAMPTZ,                   -- denormalized for the UI
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX scheduled_jobs_app_id_idx ON scheduled_jobs(app_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE job_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      UUID NOT NULL REFERENCES scheduled_jobs(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    status      TEXT NOT NULL DEFAULT 'running',  -- running | success | failed | skipped | timeout
    exit_code   INT,
    output      TEXT,                              -- bounded tail of stdout/stderr
    error       TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX job_runs_job_id_idx ON job_runs(job_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS job_runs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS scheduled_jobs;
-- +goose StatementEnd
