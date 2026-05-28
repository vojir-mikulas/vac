-- +goose Up
-- +goose StatementBegin
-- Per-service state. `status` is intentionally not CHECK-constrained — the
-- enum is owned by the Go side (internal/deploy/status.go) so we can evolve
-- it without a migration. Allowed values: created, building, deploying,
-- running, degraded, crash-loop, stopped, error, interrupted.
CREATE TABLE services (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id         UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name   TEXT NOT NULL,
    container_id   TEXT,
    exposed_port   INT,
    domain         TEXT,
    status         TEXT NOT NULL DEFAULT 'created',
    restart_count  INT  NOT NULL DEFAULT 0,
    last_exit_code INT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, service_name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX services_app_id_idx ON services(app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS services;
-- +goose StatementEnd
