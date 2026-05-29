-- +goose Up
-- +goose StatementBegin
-- Pre-aggregated request-rate rolling window: one row per (service, 10s
-- bucket). Not raw request rows. Populated by the access-log aggregator,
-- read by the per-app/per-service metrics endpoints, pruned to 24h.
CREATE TABLE request_metrics (
    id           BIGSERIAL PRIMARY KEY,
    app_id       UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    bucket_ts    TIMESTAMPTZ NOT NULL,
    requests     INT NOT NULL DEFAULT 0,
    errors       INT NOT NULL DEFAULT 0,
    bytes_out    BIGINT NOT NULL DEFAULT 0,
    UNIQUE (app_id, service_name, bucket_ts)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX request_metrics_app_ts_idx ON request_metrics(app_id, bucket_ts DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS request_metrics;
-- +goose StatementEnd
