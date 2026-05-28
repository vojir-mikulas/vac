-- +goose Up
-- +goose StatementBegin
-- Phase 2 introduces richer service / app statuses (building, degraded,
-- crash-loop, error, interrupted, …). Maintaining the CHECK constraint
-- against an evolving enum is migration-heavy; the Go side validates
-- writes (mirrors how services.status is handled).
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_status_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps
    ADD CONSTRAINT apps_status_check
    CHECK (status IN ('created', 'stopped', 'running', 'deploying', 'failed'));
-- +goose StatementEnd
