-- +goose Up
-- +goose StatementBegin
-- Per-volume disk usage, written by the diskusage.Collector background goroutine
-- (~every VAC_DISK_POLL_INTERVAL). VAC already nudges "configure a backup" on
-- services with volumes but has zero visibility into how full those volumes are;
-- on a single VPS a runaway Postgres or log-spewing app silently fills the disk.
-- This is the read side: latest sampled size per (app, service, mount), so the
-- app-detail Storage view and Prometheus can answer "which app is eating disk".
--
-- One row per mount point. used_bytes is NULL when not yet measured (a bind-mount
-- `du` that timed out or was skipped) so the UI can say "not measured" rather than
-- lie 0. source distinguishes a named volume (cheap, from `docker system df -v`)
-- from a bind mount (`du`, opt-in). Rows are upserted each tick and cascade-delete
-- with the app.
CREATE TABLE volume_usage (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    volume_name  TEXT NOT NULL DEFAULT '', -- empty for bind mounts
    mount_path   TEXT NOT NULL,            -- destination inside the container
    source       TEXT NOT NULL,            -- 'named' | 'bind'
    used_bytes   BIGINT,                   -- NULL = not yet measured
    sampled_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A mount point is unique within a service; the collector upserts on it so we
    -- keep the latest sample per mount rather than accumulating history.
    UNIQUE (app_id, service_name, mount_path)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX volume_usage_app_idx ON volume_usage(app_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-app soft disk budget in mebibytes (mirrors apps.mem_limit_mb, plan 06).
-- NULL = no limit = no per-app storage alert. "Soft" = monitor + alert only: we
-- never enforce a filesystem quota (--storage-opt size= silently no-ops on the
-- overlay2-over-ext4 default of a Pi SD card / ext4 HDD), so a hard limit would
-- lie on the most common VAC host. The collector compares the app's total volume
-- usage against this and fires EventDiskUsageHigh.
ALTER TABLE apps ADD COLUMN disk_limit_mb INT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS disk_limit_mb;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS volume_usage;
-- +goose StatementEnd
