-- +goose Up
-- +goose StatementBegin
-- Unauthenticated attempts against the control plane: failed logins and probes
-- to bogus endpoints (a scanner POSTing to random paths). These used to land in
-- audit_log and bury the operator's own activity under "Created * — failed —
-- unauthenticated" noise. The audit middleware now diverts them here instead, so
-- the activity feed stays an operator-action log and the probing has its own
-- durable record (see api/internal/server/middleware/audit.go).
--
-- There is no actor and nothing to revert — just the bare shape of the attempt:
-- method, the raw path that was hit, the outcome, and where it came from. Pruned
-- on the same window as the activity feed (activity_retention_days).
CREATE TABLE security_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    ip          TEXT,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The feed renders newest-first; pruning scans the same axis.
CREATE INDEX security_events_created_idx ON security_events(created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- "how many times has this IP probed us" — group/lookup by source.
CREATE INDEX security_events_ip_idx ON security_events(ip);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS security_events;
-- +goose StatementEnd
