-- +goose Up
-- +goose StatementBegin
-- Per-key sensitivity flag. `value` stays sealed with crypto.Box for every row
-- (defense in depth — see docs/deviations.md D9); `sensitive` only controls
-- whether the API will return the decrypted value on list. DEFAULT true keeps
-- existing rows masked.
ALTER TABLE env_vars ADD COLUMN sensitive BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE env_vars DROP COLUMN sensitive;
-- +goose StatementEnd
