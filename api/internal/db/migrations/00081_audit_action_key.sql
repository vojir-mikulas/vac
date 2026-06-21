-- +goose Up
-- +goose StatementBegin
-- Localizable activity feed: handlers now record a stable, dotted action key
-- (e.g. "deployment.rolled_back") plus its interpolation params instead of a
-- baked-in English sentence. The dashboard translates the key against its
-- `activity` catalog client-side. `summary` stays as the un-translated fallback
-- for legacy rows and the few dynamically-composed descriptions.
--
-- action_params is JSONB and is exposed to the client in the activity feed, so
-- callers must keep secrets out of it (unlike `metadata`, which is server-only).
ALTER TABLE audit_log ADD COLUMN action_key TEXT;
ALTER TABLE audit_log ADD COLUMN action_params JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN action_params;
ALTER TABLE audit_log DROP COLUMN action_key;
-- +goose StatementEnd
