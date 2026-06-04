-- +goose Up
-- +goose StatementBegin
-- stepup_verified_at records the last time this session re-proved 2FA for a
-- sensitive ("step-up") action. NULL means never. Destructive routes gate on
-- it being recent (see auth.StepUpTTL); the step-up endpoint stamps it to NOW().
ALTER TABLE sessions
    ADD COLUMN stepup_verified_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN IF EXISTS stepup_verified_at;
-- +goose StatementEnd
