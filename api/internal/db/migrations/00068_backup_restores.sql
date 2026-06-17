-- +goose Up
-- +goose StatementBegin
-- Backup restore (plan: docs/plans/backup-restore.md). One row per restore
-- attempt: read a recorded success run's artifact back from its destination and
-- replay it into the target container. Mirrors backup_runs' running→terminal
-- lifecycle. source_run_id is the backup_runs row whose artifact was replayed.
CREATE TABLE backup_restores (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id     UUID NOT NULL REFERENCES backup_configs(id) ON DELETE CASCADE,
    source_run_id UUID NOT NULL REFERENCES backup_runs(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'running',  -- running | success | failed
    error         TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX backup_restores_config_id_idx ON backup_restores(config_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS backup_restores;
-- +goose StatementEnd
