-- +goose Up
-- +goose StatementBegin
-- last_totp_step records the most recent TOTP time-step (unix_seconds / 30) that
-- was accepted for this user. The verifier rejects any step <= this value, so a
-- captured 6-digit code cannot be replayed within its ~90s (skew=1) validity
-- window. NULL means no TOTP code has been accepted yet.
ALTER TABLE users ADD COLUMN last_totp_step BIGINT;

-- Per-account brute-force lockout. Rate limiting elsewhere is per-IP only, so a
-- distributed attacker gets a fresh budget per source IP against the single admin
-- account; this caps total failed attempts per account regardless of source.
-- failed_auth_attempts counts consecutive password/second-factor failures and is
-- cleared on a fully successful login; auth_locked_until holds a future timestamp
-- while the account is locked.
ALTER TABLE users ADD COLUMN failed_auth_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN auth_locked_until TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS last_totp_step;
ALTER TABLE users DROP COLUMN IF EXISTS failed_auth_attempts;
ALTER TABLE users DROP COLUMN IF EXISTS auth_locked_until;
-- +goose StatementEnd
