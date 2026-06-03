package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// EnvVar is one app env-var record. Value is sealed by crypto.Box upstream
// (every row, sensitive or not); the store never sees plaintext. The
// `Sensitive` flag governs whether the API will return the decrypted value on
// list — see docs/deviations.md D9.
type EnvVar struct {
	ID        string
	AppID     string
	Key       string
	Value     []byte
	Sensitive bool
	// WriteOnly marks a secret as unrevealable: it can be set/replaced or
	// deleted, but its plaintext is never returned (reveal → 403). Implies
	// Sensitive. See docs/deviations.md D9.
	WriteOnly bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EnvVarInput is the write shape for ReplaceEnvVars.
type EnvVarInput struct {
	Key       string
	Value     []byte
	Sensitive bool
	WriteOnly bool
}

func (s *Store) ListEnvVarsForApp(ctx context.Context, appID string) ([]EnvVar, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, key, value, sensitive, write_only, created_at, updated_at
		FROM env_vars WHERE app_id = $1
		ORDER BY key
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnvVar
	for rows.Next() {
		var v EnvVar
		if err := rows.Scan(&v.ID, &v.AppID, &v.Key, &v.Value, &v.Sensitive, &v.WriteOnly, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReplaceEnvVars is the PUT semantics — wipe + reinsert inside a transaction
// so a partial failure leaves the prior set intact.
func (s *Store) ReplaceEnvVars(ctx context.Context, appID string, vars []EnvVarInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM env_vars WHERE app_id = $1`, appID); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.Exec(ctx, `
			INSERT INTO env_vars (app_id, key, value, sensitive, write_only) VALUES ($1, $2, $3, $4, $5)
		`, appID, v.Key, v.Value, v.Sensitive, v.WriteOnly); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpsertEnvVar sets a single env var without disturbing the others (keyed on
// app_id+key). Used by managed-database provisioning to inject the connection
// string (Track D / D2); value is sealed upstream.
func (s *Store) UpsertEnvVar(ctx context.Context, appID, key string, value []byte, sensitive bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO env_vars (app_id, key, value, sensitive) VALUES ($1, $2, $3, $4)
		ON CONFLICT (app_id, key) DO UPDATE
			SET value = EXCLUDED.value, sensitive = EXCLUDED.sensitive, updated_at = NOW()
	`, appID, key, value, sensitive)
	return err
}

// DeleteEnvVar removes a single env var (no error if absent). Used on
// managed-database deprovision to pull the injected connection string.
func (s *Store) DeleteEnvVar(ctx context.Context, appID, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM env_vars WHERE app_id = $1 AND key = $2`, appID, key)
	return err
}

// GetEnvVar fetches a single row for callers that need to verify presence —
// not part of the REST surface, but useful for tests and the pipeline.
func (s *Store) GetEnvVar(ctx context.Context, appID, key string) (EnvVar, error) {
	var v EnvVar
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, key, value, sensitive, write_only, created_at, updated_at
		FROM env_vars WHERE app_id = $1 AND key = $2
	`, appID, key).Scan(&v.ID, &v.AppID, &v.Key, &v.Value, &v.Sensitive, &v.WriteOnly, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvVar{}, ErrNotFound
	}
	return v, err
}
