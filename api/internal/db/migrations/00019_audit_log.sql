-- +goose Up
-- +goose StatementBegin
-- Durable record of every mutating action on the box: who did what, when, from
-- where, and with what outcome. Written by a central middleware (see
-- api/internal/server/middleware/audit.go) so handlers inherit auditing for
-- free; handlers add at most a one-line summary/target/metadata hook.
--
-- `actor_type` values live in Go (user|api_token|system|anonymous), kept as
-- TEXT rather than a DB enum to match the rest of the schema (status enums live
-- in code). `actor_user_id` is nullable: anonymous mutations (a failed login)
-- and system actions (auto-deploys, crashloop restarts) have no user, and a
-- later user-delete should not erase the trail.
--
-- This is the multi-user-ready successor to the derived activity feed: pruned
-- on the same window as that feed (activity_retention_days).
CREATE TABLE audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_type    TEXT NOT NULL DEFAULT 'user',
    action        TEXT NOT NULL,
    target_type   TEXT,
    target_id     TEXT,
    summary       TEXT,
    metadata      JSONB,
    ip            TEXT,
    user_agent    TEXT,
    status_code   INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The feed renders newest-first; pruning scans the same axis.
CREATE INDEX audit_log_created_idx ON audit_log(created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- "show me everything that touched this app/domain/env" — target lookups.
CREATE INDEX audit_log_target_idx ON audit_log(target_type, target_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
