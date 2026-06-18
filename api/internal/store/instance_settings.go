package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// InstanceSettings is the singleton instance-wide configuration row. BaseDomain
// is the runtime-editable override for automatic subdomains; empty means "fall
// back to the VAC_BASE_DOMAIN config value". OnboardingDismissed remembers that
// the operator closed the first-run checklist (plan 04). MaxConcurrentDeploys
// caps how many deploys the worker pool runs at once across different apps (plan
// 20); 1 is the strictly-serial default.
type InstanceSettings struct {
	BaseDomain           string
	OnboardingDismissed  bool
	MaxConcurrentDeploys int
}

// GetInstanceSettings reads the singleton row. The row is seeded by the
// migration, so a missing row is treated as empty rather than an error. A
// missing row reports MaxConcurrentDeploys=1 (the column default) so callers
// never see a nonsensical zero.
func (s *Store) GetInstanceSettings(ctx context.Context) (InstanceSettings, error) {
	var r InstanceSettings
	err := s.pool.QueryRow(ctx, `
		SELECT base_domain, onboarding_dismissed, max_concurrent_deploys
		FROM instance_settings WHERE id = 1
	`).Scan(&r.BaseDomain, &r.OnboardingDismissed, &r.MaxConcurrentDeploys)
	if errors.Is(err, pgx.ErrNoRows) {
		return InstanceSettings{MaxConcurrentDeploys: 1}, nil
	}
	return r, err
}

// SetOnboardingDismissed records whether the first-run checklist is dismissed.
func (s *Store) SetOnboardingDismissed(ctx context.Context, dismissed bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_settings (id, onboarding_dismissed, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE
			SET onboarding_dismissed = EXCLUDED.onboarding_dismissed,
			    updated_at           = NOW()
	`, dismissed)
	return err
}

// SetBaseDomain replaces the singleton base domain (already normalized by the
// caller; pass "" to clear).
func (s *Store) SetBaseDomain(ctx context.Context, baseDomain string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_settings (id, base_domain, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE
			SET base_domain = EXCLUDED.base_domain,
			    updated_at  = NOW()
	`, baseDomain)
	return err
}

// DNSSettings is the instance-wide DNS-provider configuration for custom-domain
// record automation (dns-automation plan A). Provider is ” (off) or
// 'cloudflare'; TokenEnc is the sealed API token (crypto.Box ciphertext); Zone
// is the DNS zone name records are created under.
type DNSSettings struct {
	Provider string
	TokenEnc []byte
	Zone     string
}

// Configured reports whether a usable provider + zone + token are present.
func (d DNSSettings) Configured() bool {
	return d.Provider != "" && d.Zone != "" && len(d.TokenEnc) > 0
}

// GetDNSSettings reads the singleton DNS-provider settings. A missing row (never
// seeded) reads as the empty/off configuration.
func (s *Store) GetDNSSettings(ctx context.Context) (DNSSettings, error) {
	var d DNSSettings
	err := s.pool.QueryRow(ctx, `
		SELECT dns_provider, dns_provider_token_enc, dns_zone
		FROM instance_settings WHERE id = 1
	`).Scan(&d.Provider, &d.TokenEnc, &d.Zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return DNSSettings{}, nil
	}
	return d, err
}

// SetDNSSettings replaces the singleton DNS-provider settings. The caller seals
// the token (pass nil to leave it unset / clear it when provider is cleared).
func (s *Store) SetDNSSettings(ctx context.Context, provider string, tokenEnc []byte, zone string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_settings (id, dns_provider, dns_provider_token_enc, dns_zone, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE
			SET dns_provider           = EXCLUDED.dns_provider,
			    dns_provider_token_enc = EXCLUDED.dns_provider_token_enc,
			    dns_zone               = EXCLUDED.dns_zone,
			    updated_at             = NOW()
	`, provider, tokenEnc, zone)
	return err
}

// SetMaxConcurrentDeploys records the deploy-pool concurrency. The caller must
// clamp to 1..8 (the column's CHECK constraint rejects out-of-range values).
// Takes effect on the next vac-api restart — the worker pool is sized at boot.
func (s *Store) SetMaxConcurrentDeploys(ctx context.Context, n int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_settings (id, max_concurrent_deploys, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE
			SET max_concurrent_deploys = EXCLUDED.max_concurrent_deploys,
			    updated_at             = NOW()
	`, n)
	return err
}
