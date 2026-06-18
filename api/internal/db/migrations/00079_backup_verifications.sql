-- +goose Up
-- +goose StatementBegin
-- Backup verification: a non-destructive restorability check. One row per
-- attempt — VAC restores a recorded success run's artifact into a throwaway
-- scratch database, confirms it replays cleanly, then drops it. Mirrors
-- backup_restores' running→terminal lifecycle. source_run_id is the backup_runs
-- row whose artifact was test-restored.
CREATE TABLE backup_verifications (
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
CREATE INDEX backup_verifications_config_id_idx ON backup_verifications(config_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS backup_verifications;
-- +goose StatementEnd
