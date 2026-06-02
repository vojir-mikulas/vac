-- +goose Up
-- Plan 09 Phase 3 — apex + www redirects. A domain with redirect_to set emits a
-- 308 redirect route to that hostname instead of a reverse-proxy route. A
-- "primary" domain is simply the one others redirect to — no extra flag.
-- +goose StatementBegin
ALTER TABLE domains ADD COLUMN redirect_to TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE domains DROP COLUMN IF EXISTS redirect_to;
-- +goose StatementEnd
