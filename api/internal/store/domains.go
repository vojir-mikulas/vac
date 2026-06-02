package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Domain types. Kept here so handlers and the proxy manager share one source of
// truth. Automatic subdomains are no longer stored (plan 09 F1 — they are
// derived at reconcile time); every row is now a custom domain, but the type is
// retained for forward-compatibility and the type CHECK still admits both.
const (
	DomainTypeAuto   = "auto"
	DomainTypeCustom = "custom"
)

// Domain is one custom hostname routed to a service. hostname is globally
// unique. AppID and ServiceName are empty when the domain is added but not yet
// assigned to a service (plan 09 Phase 1) — they are always both-set or
// both-empty (enforced by a CHECK).
type Domain struct {
	ID          string
	AppID       string
	ServiceName string
	Hostname    string
	Type        string
	RedirectTo  string // when set, emits a 308 redirect route to this host (Phase 3)
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Assigned reports whether the domain is bound to an app/service (and so emits
// a route). An unassigned domain is DNS-verifiable but routes nowhere.
func (d Domain) Assigned() bool { return d.AppID != "" && d.ServiceName != "" }

const domainColumns = `id, app_id, service_name, hostname, type, redirect_to, created_at, updated_at`

func scanDomain(row pgx.Row) (Domain, error) {
	var d Domain
	var appID, serviceName, redirectTo sql.NullString
	err := row.Scan(&d.ID, &appID, &serviceName, &d.Hostname, &d.Type, &redirectTo, &d.CreatedAt, &d.UpdatedAt)
	d.AppID = appID.String
	d.ServiceName = serviceName.String
	d.RedirectTo = redirectTo.String
	return d, err
}

// nullStr maps an empty string to a SQL NULL so an unassigned domain stores
// NULL (satisfying the both-or-neither CHECK) rather than "".
func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// CreateDomain inserts a hostname. appID/serviceName may be empty to create an
// unassigned domain (plan 09 Phase 1); when set they must reference an existing
// service (the composite FK) or the raw FK error surfaces. A duplicate hostname
// (the global UNIQUE) returns ErrConflict.
func (s *Store) CreateDomain(ctx context.Context, appID, serviceName, hostname, typ string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `
		INSERT INTO domains (app_id, service_name, hostname, type)
		VALUES ($1, $2, $3, $4)
		RETURNING `+domainColumns,
		nullStr(appID), nullStr(serviceName), hostname, typ))
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

// ListAllDomains returns every (custom) domain across all apps — used by the
// proxy reconcile, the status engine, and the Domains hub.
func (s *Store) ListAllDomains(ctx context.Context) ([]Domain, error) {
	return s.queryDomains(ctx, `SELECT `+domainColumns+` FROM domains ORDER BY hostname`)
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

// GetDomainByID fetches a domain by id alone — used by the Domains hub, where a
// row may be unassigned (no owning app to scope by).
func (s *Store) GetDomainByID(ctx context.Context, id string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `SELECT `+domainColumns+` FROM domains WHERE id = $1`, id))
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

// UpdateDomain changes a domain's assignment (service binding / owning app),
// hostname (plan 09 Phase 2), and redirect target (Phase 3), returning the
// updated row. Passing empty appID/serviceName unassigns it; empty redirectTo
// makes it a normal proxy domain. A duplicate hostname returns ErrConflict; a
// missing row returns ErrNotFound.
func (s *Store) UpdateDomain(ctx context.Context, id, appID, serviceName, hostname, redirectTo string) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `
		UPDATE domains
		SET app_id = $2, service_name = $3, hostname = $4, redirect_to = $5, updated_at = NOW()
		WHERE id = $1
		RETURNING `+domainColumns,
		id, nullStr(appID), nullStr(serviceName), hostname, nullStr(redirectTo)))
	if isUniqueViolation(err) {
		return Domain{}, ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	return d, err
}

// DeleteDomainByID removes a domain by id alone — used by the Domains hub where
// a row may be unassigned. Custom domains only (auto hosts aren't rows).
func (s *Store) DeleteDomainByID(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
