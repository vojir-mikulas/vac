package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Service is one row in the compose project's service list. Status enum is
// defined in Go (internal/deploy/status.go in later milestones); the DB
// stores the raw string with no CHECK.
type Service struct {
	ID           string
	AppID        string
	ServiceName  string
	ContainerID  *string
	ExposedPort  *int
	Domain       *string
	Status       string
	RestartCount int
	LastExitCode *int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UpsertService inserts a row keyed on (app_id, service_name) or updates the
// existing row. Used by the pipeline after `docker compose up` to reconcile
// the discovered service list with what's in the DB.
func (s *Store) UpsertService(ctx context.Context, appID, name string, containerID *string, exposedPort *int, status string) (Service, error) {
	var svc Service
	err := s.pool.QueryRow(ctx, `
		INSERT INTO services (app_id, service_name, container_id, exposed_port, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (app_id, service_name) DO UPDATE
			SET container_id = EXCLUDED.container_id,
			    exposed_port = EXCLUDED.exposed_port,
			    status       = EXCLUDED.status,
			    updated_at   = NOW()
		RETURNING id, app_id, service_name, container_id, exposed_port, domain,
		          status, restart_count, last_exit_code, created_at, updated_at
	`, appID, name, containerID, exposedPort, status).Scan(
		&svc.ID, &svc.AppID, &svc.ServiceName, &svc.ContainerID, &svc.ExposedPort,
		&svc.Domain, &svc.Status, &svc.RestartCount, &svc.LastExitCode,
		&svc.CreatedAt, &svc.UpdatedAt,
	)
	return svc, err
}

func (s *Store) GetService(ctx context.Context, appID, name string) (Service, error) {
	var svc Service
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, service_name, container_id, exposed_port, domain,
		       status, restart_count, last_exit_code, created_at, updated_at
		FROM services WHERE app_id = $1 AND service_name = $2
	`, appID, name).Scan(
		&svc.ID, &svc.AppID, &svc.ServiceName, &svc.ContainerID, &svc.ExposedPort,
		&svc.Domain, &svc.Status, &svc.RestartCount, &svc.LastExitCode,
		&svc.CreatedAt, &svc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return svc, err
}

func (s *Store) ListServicesForApp(ctx context.Context, appID string) ([]Service, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, service_name, container_id, exposed_port, domain,
		       status, restart_count, last_exit_code, created_at, updated_at
		FROM services WHERE app_id = $1 ORDER BY service_name
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Service
	for rows.Next() {
		var svc Service
		if err := rows.Scan(
			&svc.ID, &svc.AppID, &svc.ServiceName, &svc.ContainerID, &svc.ExposedPort,
			&svc.Domain, &svc.Status, &svc.RestartCount, &svc.LastExitCode,
			&svc.CreatedAt, &svc.UpdatedAt,
		); err != nil {
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

// IncrementServiceRestart bumps restart_count. The crash-loop monitor uses
// the returned value to decide whether to trip.
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

// SetServiceDomain is the patch endpoint backing PATCH
// /api/apps/:id/services/:name. Domain may be nil to clear.
func (s *Store) SetServiceDomain(ctx context.Context, appID, name string, domain *string, exposedPort *int) (Service, error) {
	var svc Service
	err := s.pool.QueryRow(ctx, `
		UPDATE services
		SET domain       = COALESCE($3, domain),
		    exposed_port = COALESCE($4, exposed_port),
		    updated_at   = NOW()
		WHERE app_id = $1 AND service_name = $2
		RETURNING id, app_id, service_name, container_id, exposed_port, domain,
		          status, restart_count, last_exit_code, created_at, updated_at
	`, appID, name, domain, exposedPort).Scan(
		&svc.ID, &svc.AppID, &svc.ServiceName, &svc.ContainerID, &svc.ExposedPort,
		&svc.Domain, &svc.Status, &svc.RestartCount, &svc.LastExitCode,
		&svc.CreatedAt, &svc.UpdatedAt,
	)
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
