-- +goose Up
-- +goose StatementBegin
-- Cert-expiry notification (plan 03, deviation D7). `cert_status` is advisory
-- only; the missing piece is a real per-host expiry, read back by TLS-dialling
-- the proxy with the host's SNI (see internal/certcheck).
--
-- `cert_not_after` is the leaf certificate's expiry as last observed.
-- `cert_expiry_notified_at` de-dupes the alert: it is stamped when we fire the
-- "expiring soon" notification and cleared once the cert is healthy again (a
-- successful auto-renewal), so a renewal failure alerts once per threshold
-- crossing rather than every night.
ALTER TABLE domains
    ADD COLUMN cert_not_after          TIMESTAMPTZ,
    ADD COLUMN cert_expiry_notified_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE domains
    DROP COLUMN IF EXISTS cert_not_after,
    DROP COLUMN IF EXISTS cert_expiry_notified_at;
-- +goose StatementEnd
