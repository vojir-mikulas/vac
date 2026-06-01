-- +goose Up
-- +goose StatementBegin
-- Onboarding wizard (plan 04). The guided first-run checklist (set base domain →
-- point DNS → first deploy) is dismissible and "dismiss permanently"; this flag
-- on the singleton settings row remembers that choice server-side so it survives
-- a browser change. Defaults FALSE so a fresh instance shows the checklist.
ALTER TABLE instance_settings
    ADD COLUMN onboarding_dismissed BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instance_settings
    DROP COLUMN IF EXISTS onboarding_dismissed;
-- +goose StatementEnd
