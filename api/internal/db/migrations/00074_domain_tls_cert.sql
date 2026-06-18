-- +goose Up
-- +goose StatementBegin
-- Bring-your-own TLS certificate (docs/plans/dns-automation-and-byo-cert.md, Part
-- B). For a domain where ACME HTTP-challenge can't work (wildcard / internal) the
-- operator uploads a cert + key, and Caddy serves it instead of issuing one.
--
-- The leaf+chain PEM is public material, stored plaintext. Only the private key
-- is a bearer secret, so it is sealed with VAC_MASTER_KEY (crypto.Box ciphertext)
-- like ssh_keys.private_key / smtp_password_enc. tls_cert_source flips a host
-- between ACME (the default) and an uploaded cert; uploaded always wins (Caddy
-- has the cert in cache, so on-demand issuance never fires).
ALTER TABLE domains
    ADD COLUMN tls_cert_pem         TEXT,
    ADD COLUMN tls_key_enc          BYTEA,
    ADD COLUMN tls_cert_source      TEXT NOT NULL DEFAULT 'acme'
        CHECK (tls_cert_source IN ('acme', 'uploaded')),
    ADD COLUMN tls_cert_uploaded_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE domains
    DROP COLUMN IF EXISTS tls_cert_pem,
    DROP COLUMN IF EXISTS tls_key_enc,
    DROP COLUMN IF EXISTS tls_cert_source,
    DROP COLUMN IF EXISTS tls_cert_uploaded_at;
-- +goose StatementEnd
