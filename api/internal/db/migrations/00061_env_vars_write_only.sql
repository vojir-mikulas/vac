-- +goose Up
-- +goose StatementBegin
-- Opt-in "write-only / no-reveal" mode for a secret. `value` stays sealed with
-- crypto.Box for every row (defense in depth — see docs/deviations.md D9); a
-- write-only row is set/replaceable but its plaintext is never returned (reveal
-- → 403). DEFAULT false keeps every existing row behaving exactly as today —
-- the flag is purely opt-in. Implies `sensitive` (normalized at the handler).
ALTER TABLE env_vars ADD COLUMN write_only BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE env_vars DROP COLUMN write_only;
-- +goose StatementEnd
