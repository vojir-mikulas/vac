package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// EnvVar is one app env-var record. Value is sealed by crypto.Box upstream;
// the store never sees plaintext.
type EnvVar struct {
	ID        string
	AppID     string
	Key       string
	Value     []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EnvVarInput is the write shape for ReplaceEnvVars.
type EnvVarInput struct {
	Key   string
	Value []byte
}

func (s *Store) ListEnvVarsForApp(ctx context.Context, appID string) ([]EnvVar, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, key, value, created_at, updated_at
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
		if err := rows.Scan(&v.ID, &v.AppID, &v.Key, &v.Value, &v.CreatedAt, &v.UpdatedAt); err != nil {
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
			INSERT INTO env_vars (app_id, key, value) VALUES ($1, $2, $3)
		`, appID, v.Key, v.Value); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetEnvVar fetches a single row for callers that need to verify presence —
// not part of the REST surface, but useful for tests and the pipeline.
func (s *Store) GetEnvVar(ctx context.Context, appID, key string) (EnvVar, error) {
	var v EnvVar
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, key, value, created_at, updated_at
		FROM env_vars WHERE app_id = $1 AND key = $2
	`, appID, key).Scan(&v.ID, &v.AppID, &v.Key, &v.Value, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvVar{}, ErrNotFound
	}
	return v, err
}
