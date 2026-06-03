package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ManagedDatabase is one app-owned database on a shared engine (Track D / D2).
// SecretEnc is the crypto.Box-sealed connection string; the store never sees
// plaintext.
type ManagedDatabase struct {
	ID         string
	AppID      string
	Engine     string // postgres | mariadb | mongo | redis
	DBName     string
	RoleName   *string // NULL for redis
	SecretEnc  []byte
	EnvVarName string
	Status     string // provisioning | ready | error
	Error      *string
	CreatedAt  time.Time
}

const managedDBColumns = `id, app_id, engine, db_name, role_name, secret_enc,
	env_var_name, status, error, created_at`

func scanManagedDB(row pgx.Row) (ManagedDatabase, error) {
	var m ManagedDatabase
	err := row.Scan(
		&m.ID, &m.AppID, &m.Engine, &m.DBName, &m.RoleName, &m.SecretEnc,
		&m.EnvVarName, &m.Status, &m.Error, &m.CreatedAt,
	)
	return m, err
}

// CreateManagedDatabase inserts a row in the `provisioning` state. A duplicate
// (app, engine, db_name) collides → ErrConflict.
func (s *Store) CreateManagedDatabase(ctx context.Context, appID, engine, dbName string, roleName *string, secretEnc []byte, envVarName string) (ManagedDatabase, error) {
	m, err := scanManagedDB(s.pool.QueryRow(ctx, `
		INSERT INTO managed_databases (app_id, engine, db_name, role_name, secret_enc, env_var_name, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'provisioning')
		RETURNING `+managedDBColumns,
		appID, engine, dbName, roleName, secretEnc, envVarName))
	if isUniqueViolation(err) {
		return ManagedDatabase{}, ErrConflict
	}
	return m, err
}

// SetManagedDatabaseStatus moves a row to ready/error. errMsg is recorded only
// for the error state (pass nil otherwise).
func (s *Store) SetManagedDatabaseStatus(ctx context.Context, id, status string, errMsg *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE managed_databases SET status = $2, error = $3 WHERE id = $1
	`, id, status, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetManagedDatabase(ctx context.Context, id string) (ManagedDatabase, error) {
	m, err := scanManagedDB(s.pool.QueryRow(ctx, `
		SELECT `+managedDBColumns+` FROM managed_databases WHERE id = $1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedDatabase{}, ErrNotFound
	}
	return m, err
}

func (s *Store) ListManagedDatabasesForApp(ctx context.Context, appID string) ([]ManagedDatabase, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+managedDBColumns+` FROM managed_databases WHERE app_id = $1 ORDER BY created_at
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDatabase
	for rows.Next() {
		m, err := scanManagedDB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ManagedDatabaseWithApp is a managed DB joined to its owning app — the shape the
// box-wide Database section reads (plan 20). The control-plane vac-db has no row
// here (it has no app); the inventory adds it synthetically.
type ManagedDatabaseWithApp struct {
	ManagedDatabase
	AppSlug string
	AppName string
}

// ListAllManagedDatabases returns every managed DB across all apps, joined to its
// owning app, for the box-wide inventory. Ordered by engine then app slug so the
// caller can group without re-sorting.
func (s *Store) ListAllManagedDatabases(ctx context.Context) ([]ManagedDatabaseWithApp, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.app_id, m.engine, m.db_name, m.role_name, m.secret_enc,
		       m.env_var_name, m.status, m.error, m.created_at, a.slug, a.name
		FROM managed_databases m
		JOIN apps a ON a.id = m.app_id
		ORDER BY m.engine, a.slug, m.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDatabaseWithApp
	for rows.Next() {
		var m ManagedDatabaseWithApp
		if err := rows.Scan(
			&m.ID, &m.AppID, &m.Engine, &m.DBName, &m.RoleName, &m.SecretEnc,
			&m.EnvVarName, &m.Status, &m.Error, &m.CreatedAt, &m.AppSlug, &m.AppName,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountManagedDatabasesByEngine returns how many managed DBs use a given engine
// — the provisioner uses it to decide whether a shared engine container is still
// needed after a deprovision.
func (s *Store) CountManagedDatabasesByEngine(ctx context.Context, engine string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM managed_databases WHERE engine = $1`, engine).Scan(&n)
	return n, err
}

func (s *Store) DeleteManagedDatabase(ctx context.Context, appID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM managed_databases WHERE id = $1 AND app_id = $2`, id, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
