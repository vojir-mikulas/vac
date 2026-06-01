-- +goose Up
-- +goose StatementBegin
-- Curated revert (plan 11, Part 2). The audit_log already records every mutating
-- action; this lets a *safely-invertible* one carry the inverse so it can be
-- undone with one click.
--
-- `revertable` is set by the handler (via the audit package) only for the curated
-- set where a clean inverse exists (env replace, base-domain, app-config update);
-- the before-snapshot lives in the existing `metadata` JSONB under a "before" key.
-- Destructive actions (instance reset, hard app delete) leave it FALSE and the UI
-- greys out undo. `reverted_at` is stamped once an entry has been undone, so the
-- UI shows "reverted" and refuses a double-undo.
ALTER TABLE audit_log
    ADD COLUMN revertable  BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN reverted_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE audit_log
    DROP COLUMN IF EXISTS revertable,
    DROP COLUMN IF EXISTS reverted_at;
-- +goose StatementEnd
