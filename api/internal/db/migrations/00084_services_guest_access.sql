-- +goose Up
-- +goose StatementBegin
-- guest_access_code_enc holds the (crypto.Box-sealed) shared access code that
-- lets non-operators past the VAC login gate for THIS service — "share this one
-- internal tool with friends" without giving them any VAC dashboard access. Per
-- service (not per app) so each guarded container is shared independently, matching
-- the per-service requires_auth gate. NULL = no guest access (only operators pass).
-- Stored sealed like the webhook secret; the store only moves ciphertext, and the
-- column is surfaced to reads only as a boolean (guest_access_code_enc IS NOT NULL).
ALTER TABLE services ADD COLUMN guest_access_code_enc BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN guest_access_code_enc;
-- +goose StatementEnd
