package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Domain types and cert-status values. Kept here so handlers and the proxy
// manager share one source of truth.
const (
	DomainTypeAuto   = "auto"
	DomainTypeCustom = "custom"

	CertStatusPending = "pending"
	CertStatusActive  = "active"
	CertStatusError   = "error"
)

// Domain is one hostname routed to a service. hostname is globally unique.
type Domain struct {
	ID          string
	AppID       string
	ServiceName string
	Hostname    string
	Type        string
	CertStatus  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const domainColumns = `id, app_id, service_name, hostname, type, cert_status, created_at, updated_at`

func scanDomain(row pgx.Row) (Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.AppID, &d.ServiceName, &d.Hostname, &d.Type, &d.CertStatus, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// CreateDomain inserts a hostname for a service. A duplicate hostname (the
// global UNIQUE) returns ErrConflict. A missing service (the composite FK)
// surfaces the raw FK error to the caller.
func (s *Store) CreateDomain(ctx context.Context, appID, serviceName, hostname, typ string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `
		INSERT INTO domains (app_id, service_name, hostname, type)
		VALUES ($1, $2, $3, $4)
		RETURNING `+domainColumns,
		appID, serviceName, hostname, typ))
	if isUniqueViolation(err) {
		return Domain{}, ErrConflict
	}
	return d, err
}

func (s *Store) ListDomainsByApp(ctx context.Context, appID string) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM domains WHERE app_id = $1 ORDER BY hostname`, appID)
}

// ListDomainsByService returns the domains attached to one service of an app.
func (s *Store) ListDomainsByService(ctx context.Context, appID, serviceName string) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM domains WHERE app_id = $1 AND service_name = $2 ORDER BY hostname`, appID, serviceName)
}

// ListAllDomains returns every domain across all apps — used by the proxy
// reconcile on boot.
func (s *Store) ListAllDomains(ctx context.Context) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM domains ORDER BY app_id, hostname`)
}

func (s *Store) queryDomains(ctx context.Context, sql string, args ...any) ([]Domain, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDomainByHostname backs the on-demand-TLS ask endpoint — returns ErrNotFound
// when the host isn't known to VAC (so Caddy refuses to issue a cert).
func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `SELECT `+domainColumns+` FROM domains WHERE hostname = $1`, hostname))
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	return d, err
}

// GetDomain fetches by id, scoped to an app so one app can't address another's
// domain.
func (s *Store) GetDomain(ctx context.Context, appID, id string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `SELECT `+domainColumns+` FROM domains WHERE id = $1 AND app_id = $2`, id, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	return d, err
}

// DeleteDomain removes a domain by id, scoped to its app.
func (s *Store) DeleteDomain(ctx context.Context, appID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1 AND app_id = $2`, id, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCertStatus updates the advisory cert_status for a domain (best-effort,
// polled from Caddy). A missing row is not an error.
func (s *Store) SetCertStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE domains SET cert_status = $2, updated_at = NOW() WHERE id = $1`, id, status)
	return err
}

// DomainCert is the slim per-host cert state the expiry checker (plan 03) works
// with — deliberately separate from the full Domain row so the hot scan path
// stays untouched. NotAfter / NotifiedAt are nil until first observed.
type DomainCert struct {
	ID         string
	Hostname   string
	NotAfter   *time.Time
	NotifiedAt *time.Time
}

// ListDomainCerts returns every domain's cert-expiry state for the background
// checker to refresh.
func (s *Store) ListDomainCerts(ctx context.Context) ([]DomainCert, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, hostname, cert_not_after, cert_expiry_notified_at
		FROM domains ORDER BY hostname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DomainCert
	for rows.Next() {
		var c DomainCert
		if err := rows.Scan(&c.ID, &c.Hostname, &c.NotAfter, &c.NotifiedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCertNotAfter records the leaf certificate's observed expiry for a host.
func (s *Store) SetCertNotAfter(ctx context.Context, id string, notAfter time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE domains SET cert_not_after = $2, updated_at = NOW() WHERE id = $1`, id, notAfter)
	return err
}

// MarkCertExpiryNotified stamps the expiry-alert de-dupe timestamp so the same
// near-expiry cert isn't re-alerted every check.
func (s *Store) MarkCertExpiryNotified(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE domains SET cert_expiry_notified_at = $2, updated_at = NOW() WHERE id = $1`, id, at)
	return err
}

// ClearCertExpiryNotified resets the de-dupe stamp once a cert is healthy again
// (renewed), so a future expiry alerts afresh.
func (s *Store) ClearCertExpiryNotified(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE domains SET cert_expiry_notified_at = NULL, updated_at = NOW() WHERE id = $1 AND cert_expiry_notified_at IS NOT NULL`, id)
	return err
}
