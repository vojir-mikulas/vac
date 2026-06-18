-- +goose Up
-- +goose StatementBegin
-- request_metrics has only request_metrics_app_ts_idx (app_id, bucket_ts DESC),
-- whose leading column is app_id. Two hot paths filter by bucket_ts ALONE and so
-- can't use it: the host-wide request sparkline (QueryHostRequestSeries) and the
-- nightly retention prune (DeleteRequestMetricsOlderThan). Both seq-scan the
-- table today. The window is pruned to ~24h of 10s buckets so the table stays
-- small, but a dedicated bucket_ts index removes the scan and keeps the prune
-- cheap as traffic grows.
CREATE INDEX IF NOT EXISTS request_metrics_bucket_ts_idx ON request_metrics(bucket_ts);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS request_metrics_bucket_ts_idx;
-- +goose StatementEnd
