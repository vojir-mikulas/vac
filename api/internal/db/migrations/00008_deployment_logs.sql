-- +goose Up
-- +goose StatementBegin
-- Build-time logs from `docker compose build`. Kept permanently with the
-- deployment record (per mvp.md § Log Retention). `service_name` is nullable
-- because pipeline-level lines (clone, detect, env-render) are not scoped to
-- a service. `stream` is 'stdout' | 'stderr' | 'system'.
CREATE TABLE deployment_logs (
    id            BIGSERIAL PRIMARY KEY,
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    service_name  TEXT,
    stream        TEXT NOT NULL,
    message       TEXT NOT NULL,
    ts            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX deployment_logs_deployment_ts_idx
    ON deployment_logs(deployment_id, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deployment_logs;
-- +goose StatementEnd
