package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres SQLSTATE 23505 = unique_violation. Used to translate slug
// collisions into ErrConflict at the store layer.
const pgUniqueViolation = "23505"

type App struct {
	ID          string
	Name        string
	Slug        string
	GitURL      string
	GitBranch   string
	ComposeFile string
	// BuildKind selects the deploy adapter (auto|compose|dockerfile|framework|
	// static); BuildConfig holds its adapter-specific JSON knobs. The store
	// keeps BuildConfig opaque — the adapter/handler layers own its shape.
	BuildKind   string
	BuildConfig json.RawMessage
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) CreateApp(ctx context.Context, name, slug, gitURL, gitBranch, composeFile, buildKind string, buildConfig json.RawMessage) (App, error) {
	if buildKind == "" {
		buildKind = "auto"
	}
	if len(buildConfig) == 0 {
		buildConfig = json.RawMessage("{}")
	}
	var a App
	err := s.pool.QueryRow(ctx, `
		INSERT INTO apps (name, slug, git_url, git_branch, compose_file, build_kind, build_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, created_at, updated_at
	`, name, slug, gitURL, gitBranch, composeFile, buildKind, buildConfig).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return App{}, ErrConflict
	}
	return a, err
}

// GetAppBySlug is used by the crash-loop monitor to translate a Docker
// compose project label back to a VAC app row.
func (s *Store) GetAppBySlug(ctx context.Context, slug string) (App, error) {
	var a App
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, created_at, updated_at
		FROM apps WHERE slug = $1
	`, slug).Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

func (s *Store) GetApp(ctx context.Context, id string) (App, error) {
	var a App
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, created_at, updated_at
		FROM apps WHERE id = $1
	`, id).Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

func (s *Store) ListApps(ctx context.Context) ([]App, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, created_at, updated_at
		FROM apps ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApp applies a partial patch: any of the fields that are nil stay as
// they were. Returns the row post-update. Slug is intentionally not patchable —
// once set it's a stable URL handle.
func (s *Store) UpdateApp(ctx context.Context, id string, name, gitURL, gitBranch, composeFile, buildKind *string, buildConfig json.RawMessage) (App, error) {
	var a App
	// buildConfig is a JSONB column, so a nil RawMessage must reach the query as
	// a typed nil (not an empty []byte) for COALESCE to keep the existing value.
	var bc any
	if buildConfig != nil {
		bc = buildConfig
	}
	err := s.pool.QueryRow(ctx, `
		UPDATE apps SET
			name         = COALESCE($2, name),
			git_url      = COALESCE($3, git_url),
			git_branch   = COALESCE($4, git_branch),
			compose_file = COALESCE($5, compose_file),
			build_kind   = COALESCE($6, build_kind),
			build_config = COALESCE($7, build_config),
			updated_at   = NOW()
		WHERE id = $1
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, created_at, updated_at
	`, id, name, gitURL, gitBranch, composeFile, buildKind, bc).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

// SetAppStatus is the lightweight write the deployment pipeline uses to
// reflect the derived stack status into the apps row. Valid values are
// owned by the Go side (no DB CHECK after 00011).
func (s *Store) SetAppStatus(ctx context.Context, id, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE apps SET status = $2, updated_at = NOW() WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteApp(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
