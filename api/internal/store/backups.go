package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// BackupConfig is one per-service backup recipe (Track D / D1). DestConfig is
// crypto.Box-sealed JSON (bucket/endpoint/keys) — the store never sees plaintext
// credentials, exactly like env_vars.value and apps.webhook_secret_enc.
type BackupConfig struct {
	ID          string
	AppID       string
	ServiceName string
	Command     string
	Frequency   string // daily | weekly
	HourOfDay   int
	DayOfWeek   *int   // 0-6 (Sun=0); NULL for daily
	Destination string // local | s3
	DestConfig  []byte
	KeepCount   int
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BackupConfigInput is the write shape for create/update.
type BackupConfigInput struct {
	ServiceName string
	Command     string
	Frequency   string
	HourOfDay   int
	DayOfWeek   *int
	Destination string
	DestConfig  []byte
	KeepCount   int
	Enabled     bool
}

// BackupRun is one execution of a BackupConfig.
type BackupRun struct {
	ID          string
	ConfigID    string
	StartedAt   time.Time
	FinishedAt  *time.Time
	Status      string // running | success | failed
	SizeBytes   *int64
	ArtifactKey *string
	Error       *string
}

const backupConfigColumns = `id, app_id, service_name, command, frequency,
	hour_of_day, day_of_week, destination, dest_config, keep_count, enabled,
	created_at, updated_at`

func scanBackupConfig(row pgx.Row) (BackupConfig, error) {
	var c BackupConfig
	err := row.Scan(
		&c.ID, &c.AppID, &c.ServiceName, &c.Command, &c.Frequency,
		&c.HourOfDay, &c.DayOfWeek, &c.Destination, &c.DestConfig, &c.KeepCount,
		&c.Enabled, &c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

// CreateBackupConfig inserts a new per-service backup config. A second config
// for the same (app, service) collides on the UNIQUE constraint → ErrConflict.
func (s *Store) CreateBackupConfig(ctx context.Context, appID string, in BackupConfigInput) (BackupConfig, error) {
	c, err := scanBackupConfig(s.pool.QueryRow(ctx, `
		INSERT INTO backup_configs
			(app_id, service_name, command, frequency, hour_of_day, day_of_week, destination, dest_config, keep_count, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+backupConfigColumns,
		appID, in.ServiceName, in.Command, in.Frequency, in.HourOfDay, in.DayOfWeek,
		in.Destination, in.DestConfig, in.KeepCount, in.Enabled))
	if isUniqueViolation(err) {
		return BackupConfig{}, ErrConflict
	}
	return c, err
}

// UpdateBackupConfig overwrites the mutable fields of an existing config.
func (s *Store) UpdateBackupConfig(ctx context.Context, appID, configID string, in BackupConfigInput) (BackupConfig, error) {
	c, err := scanBackupConfig(s.pool.QueryRow(ctx, `
		UPDATE backup_configs SET
			command     = $3,
			frequency   = $4,
			hour_of_day = $5,
			day_of_week = $6,
			destination = $7,
			dest_config = $8,
			keep_count  = $9,
			enabled     = $10,
			updated_at  = NOW()
		WHERE id = $1 AND app_id = $2
		RETURNING `+backupConfigColumns,
		configID, appID, in.Command, in.Frequency, in.HourOfDay, in.DayOfWeek,
		in.Destination, in.DestConfig, in.KeepCount, in.Enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupConfig{}, ErrNotFound
	}
	return c, err
}

func (s *Store) GetBackupConfig(ctx context.Context, configID string) (BackupConfig, error) {
	c, err := scanBackupConfig(s.pool.QueryRow(ctx, `
		SELECT `+backupConfigColumns+` FROM backup_configs WHERE id = $1
	`, configID))
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupConfig{}, ErrNotFound
	}
	return c, err
}

func (s *Store) ListBackupConfigsForApp(ctx context.Context, appID string) ([]BackupConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+backupConfigColumns+` FROM backup_configs WHERE app_id = $1 ORDER BY service_name
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupConfig
	for rows.Next() {
		c, err := scanBackupConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListEnabledBackupConfigs returns every enabled config across all apps — the
// scheduler's working set for computing the next due time.
func (s *Store) ListEnabledBackupConfigs(ctx context.Context) ([]BackupConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+backupConfigColumns+` FROM backup_configs WHERE enabled = TRUE ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupConfig
	for rows.Next() {
		c, err := scanBackupConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountBackupConfigs reports how many configs exist — main.go uses it to decide
// whether to start the scheduler goroutine at boot (zero-footprint when none).
func (s *Store) CountBackupConfigs(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM backup_configs`).Scan(&n)
	return n, err
}

func (s *Store) DeleteBackupConfig(ctx context.Context, appID, configID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM backup_configs WHERE id = $1 AND app_id = $2`, configID, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteBackupConfigForService removes the config tied to a managed DB on
// deprovision. No-op (no error) when none exists.
func (s *Store) DeleteBackupConfigForService(ctx context.Context, appID, service string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM backup_configs WHERE app_id = $1 AND service_name = $2`, appID, service)
	return err
}

// CreateBackupRun opens a run row in the `running` state; FinishBackupRun closes
// it. The pair mirrors the deployment running→terminal lifecycle.
func (s *Store) CreateBackupRun(ctx context.Context, configID string) (BackupRun, error) {
	var r BackupRun
	err := s.pool.QueryRow(ctx, `
		INSERT INTO backup_runs (config_id, status) VALUES ($1, 'running')
		RETURNING id, config_id, started_at, finished_at, status, size_bytes, artifact_key, error
	`, configID).Scan(&r.ID, &r.ConfigID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SizeBytes, &r.ArtifactKey, &r.Error)
	return r, err
}

// FinishBackupRun records the terminal state. errMsg is empty on success.
func (s *Store) FinishBackupRun(ctx context.Context, runID, status string, sizeBytes *int64, artifactKey *string, errMsg *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE backup_runs
		SET status = $2, size_bytes = $3, artifact_key = $4, error = $5, finished_at = NOW()
		WHERE id = $1
	`, runID, status, sizeBytes, artifactKey, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetBackupRun fetches one run — backs the artifact-download endpoint.
func (s *Store) GetBackupRun(ctx context.Context, runID string) (BackupRun, error) {
	var r BackupRun
	err := s.pool.QueryRow(ctx, `
		SELECT id, config_id, started_at, finished_at, status, size_bytes, artifact_key, error
		FROM backup_runs WHERE id = $1
	`, runID).Scan(&r.ID, &r.ConfigID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SizeBytes, &r.ArtifactKey, &r.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupRun{}, ErrNotFound
	}
	return r, err
}

// ListBackupRuns returns the most recent runs for a config, newest first.
func (s *Store) ListBackupRuns(ctx context.Context, configID string, limit int) ([]BackupRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, config_id, started_at, finished_at, status, size_bytes, artifact_key, error
		FROM backup_runs WHERE config_id = $1 ORDER BY started_at DESC LIMIT $2
	`, configID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRun
	for rows.Next() {
		var r BackupRun
		if err := rows.Scan(&r.ID, &r.ConfigID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SizeBytes, &r.ArtifactKey, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestBackupRun returns the newest run for a config, or ErrNotFound if the
// config has never run. Backs the "download latest" affordance.
func (s *Store) LatestBackupRun(ctx context.Context, configID string) (BackupRun, error) {
	var r BackupRun
	err := s.pool.QueryRow(ctx, `
		SELECT id, config_id, started_at, finished_at, status, size_bytes, artifact_key, error
		FROM backup_runs WHERE config_id = $1 ORDER BY started_at DESC LIMIT 1
	`, configID).Scan(&r.ID, &r.ConfigID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SizeBytes, &r.ArtifactKey, &r.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupRun{}, ErrNotFound
	}
	return r, err
}
