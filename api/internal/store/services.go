package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Service is one row in the compose project's service list. Status enum is
// defined in Go (internal/deploy/status.go); the DB stores the raw string
// with no CHECK.
type Service struct {
	ID             string
	AppID          string
	ServiceName    string
	ContainerID    *string
	ExposedPort    *int // host-published port (Phase 2; diagnostics only now)
	InternalPort   *int // container port — what Caddy dials over vac-edge
	HealthPath     *string
	Status         string
	RestartCount   int
	LastExitCode   *int
	OOMKilledCount int
	// HasVolumes is true when the service declares a persistent volume (any
	// volume mount other than the Docker socket). Recomputed from the compose
	// file on every deploy; drives the dashboard's backup nudge.
	HasVolumes bool
	// IsPrivate, when true, forces the service internal-only: the proxy assigns
	// it no auto-domain and routes no custom domain to it, even when its image or
	// compose file exposes a port. Operator-set via PatchAppService; survives
	// redeploys (UpsertService never writes it).
	IsPrivate bool
	// RequiresAuth, when true, puts the service's HTTP route behind the VAC login
	// gate: Caddy fronts it with a forward_auth handler so only logged-in VAC
	// users reach it (see internal/guard). Operator-set via PatchAppService;
	// survives redeploys (UpsertService never writes it). Orthogonal to IsPrivate
	// — a private service has no route to guard.
	RequiresAuth bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const serviceColumns = `id, app_id, service_name, container_id, exposed_port,
	internal_port, health_path, status, restart_count, last_exit_code,
	oom_killed_count, has_volumes, is_private, requires_auth, created_at, updated_at`

func scanService(row pgx.Row) (Service, error) {
	var svc Service
	err := row.Scan(
		&svc.ID, &svc.AppID, &svc.ServiceName, &svc.ContainerID, &svc.ExposedPort,
		&svc.InternalPort, &svc.HealthPath, &svc.Status, &svc.RestartCount,
		&svc.LastExitCode, &svc.OOMKilledCount, &svc.HasVolumes, &svc.IsPrivate, &svc.RequiresAuth, &svc.CreatedAt, &svc.UpdatedAt,
	)
	return svc, err
}

// UpsertService inserts a row keyed on (app_id, service_name) or updates the
// existing row. Used by the pipeline after `docker compose up` to reconcile the
// discovered service list with the DB. internal_port is COALESCE'd so a deploy
// that can't detect the container port (no published/exposed mapping) preserves
// any operator-set value rather than nulling it.
//
// Caveat (operator overrides are not sticky): when a deploy *does* detect a
// container port, EXCLUDED.internal_port is non-null and wins, so a value an
// operator set via PatchAppService is overwritten on the next deploy. That's
// acceptable today — the override exists mainly for repos that only `expose` a
// port (detection returns 0, override preserved). Making overrides survive a
// redeploy would need an explicit "operator-set" flag column.
func (s *Store) UpsertService(ctx context.Context, appID, name string, containerID *string, exposedPort, internalPort *int, status string, hasVolumes bool) (Service, error) {
	return scanService(s.pool.QueryRow(ctx, `
		INSERT INTO services (app_id, service_name, container_id, exposed_port, internal_port, status, has_volumes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (app_id, service_name) DO UPDATE
			SET container_id  = EXCLUDED.container_id,
			    exposed_port  = EXCLUDED.exposed_port,
			    internal_port = COALESCE(EXCLUDED.internal_port, services.internal_port),
			    status        = EXCLUDED.status,
			    has_volumes   = EXCLUDED.has_volumes,
			    updated_at    = NOW()
		RETURNING `+serviceColumns,
		appID, name, containerID, exposedPort, internalPort, status, hasVolumes))
}

func (s *Store) GetService(ctx context.Context, appID, name string) (Service, error) {
	svc, err := scanService(s.pool.QueryRow(ctx, `
		SELECT `+serviceColumns+` FROM services WHERE app_id = $1 AND service_name = $2
	`, appID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return svc, err
}

// ListServiceProjects returns one (app slug, service name) pair per persisted
// service across all apps. The retention pruner uses these to enumerate
// (compose project, service) pairs for per-service image pruning — the compose
// project is "vac-"+slug (mirrors deploy.composeProject).
func (s *Store) ListServiceProjects(ctx context.Context) ([]struct{ Slug, ServiceName string }, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.slug, s.service_name
		FROM services s JOIN apps a ON a.id = s.app_id
		ORDER BY a.slug, s.service_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ Slug, ServiceName string }
	for rows.Next() {
		var p struct{ Slug, ServiceName string }
		if err := rows.Scan(&p.Slug, &p.ServiceName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListServicesForApp(ctx context.Context, appID string) ([]Service, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+serviceColumns+` FROM services WHERE app_id = $1 ORDER BY service_name
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Service
	for rows.Next() {
		svc, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

// UpdateServiceStatus is the lightweight write used by the crash-loop monitor
// and the lifecycle handlers. Pass exitCode=nil to leave it unchanged.
func (s *Store) UpdateServiceStatus(ctx context.Context, appID, name, status string, exitCode *int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE services
		SET status = $3,
		    last_exit_code = COALESCE($4, last_exit_code),
		    updated_at = NOW()
		WHERE app_id = $1 AND service_name = $2
	`, appID, name, status, exitCode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IncrementServiceRestart bumps restart_count. The crash-loop monitor uses the
// returned value to decide whether to trip.
func (s *Store) IncrementServiceRestart(ctx context.Context, appID, name string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		UPDATE services
		SET restart_count = restart_count + 1, updated_at = NOW()
		WHERE app_id = $1 AND service_name = $2
		RETURNING restart_count
	`, appID, name).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// IncrementServiceOOM bumps oom_killed_count and records the exit code. The
// crash-loop monitor calls this when a container death is confirmed (via docker
// inspect) to be an OOM kill, so the UI can label it distinctly from an ordinary
// crash. Status is left alone — the container is restart:always, so its
// lifecycle status (running again, or crash-loop if it trips) is owned
// elsewhere; OOM is surfaced through the count, not a status flip.
func (s *Store) IncrementServiceOOM(ctx context.Context, appID, name string, exitCode *int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		UPDATE services
		SET oom_killed_count = oom_killed_count + 1,
		    last_exit_code   = COALESCE($3, last_exit_code),
		    updated_at       = NOW()
		WHERE app_id = $1 AND service_name = $2
		RETURNING oom_killed_count
	`, appID, name, exitCode).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// SetServiceConfig backs PATCH /api/apps/:id/services/:name. Each pointer is
// COALESCE'd so a nil leaves the existing value untouched (partial update).
func (s *Store) SetServiceConfig(ctx context.Context, appID, name string, exposedPort, internalPort *int, healthPath *string, isPrivate, requiresAuth *bool) (Service, error) {
	svc, err := scanService(s.pool.QueryRow(ctx, `
		UPDATE services
		SET exposed_port  = COALESCE($3, exposed_port),
		    internal_port = COALESCE($4, internal_port),
		    health_path   = COALESCE($5, health_path),
		    is_private    = COALESCE($6, is_private),
		    requires_auth = COALESCE($7, requires_auth),
		    updated_at    = NOW()
		WHERE app_id = $1 AND service_name = $2
		RETURNING `+serviceColumns,
		appID, name, exposedPort, internalPort, healthPath, isPrivate, requiresAuth))
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return svc, err
}

// DeleteService removes a service row — used by the pipeline when a compose
// project no longer declares a service that previously existed.
func (s *Store) DeleteService(ctx context.Context, appID, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM services WHERE app_id = $1 AND service_name = $2`, appID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
