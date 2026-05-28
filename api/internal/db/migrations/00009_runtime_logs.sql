-- +goose Up
-- +goose StatementBegin
-- Container stdout/stderr captured by `docker logs --follow`. Pruned by the
-- retention goroutine (default 7 days). `stream` is 'stdout' | 'stderr' |
-- 'system' (where 'system' is used for VAC-emitted notices like
-- "crash-loop: stopped after N restarts").
CREATE TABLE runtime_logs (
    id           BIGSERIAL PRIMARY KEY,
    app_id       UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    stream       TEXT NOT NULL,
    message      TEXT NOT NULL,
    ts           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX runtime_logs_app_ts_idx ON runtime_logs(app_id, ts DESC);
CREATE INDEX runtime_logs_ts_idx     ON runtime_logs(ts);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS runtime_logs;
-- +goose StatementEnd
