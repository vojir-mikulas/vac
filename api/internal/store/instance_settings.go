package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// InstanceSettings is the singleton instance-wide configuration row. BaseDomain
// is the runtime-editable override for automatic subdomains; empty means "fall
// back to the VAC_BASE_DOMAIN config value". OnboardingDismissed remembers that
// the operator closed the first-run checklist (plan 04).
type InstanceSettings struct {
	BaseDomain          string
	OnboardingDismissed bool
}

// GetInstanceSettings reads the singleton row. The row is seeded by the
// migration, so a missing row is treated as empty rather than an error.
func (s *Store) GetInstanceSettings(ctx context.Context) (InstanceSettings, error) {
	var r InstanceSettings
	err := s.pool.QueryRow(ctx, `
		SELECT base_domain, onboarding_dismissed FROM instance_settings WHERE id = 1
	`).Scan(&r.BaseDomain, &r.OnboardingDismissed)
	if errors.Is(err, pgx.ErrNoRows) {
		return InstanceSettings{}, nil
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
