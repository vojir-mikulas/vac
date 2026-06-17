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

// App source kinds (Track D / D3). A `git` app clones from GitURL; a `template`
// app materializes embedded add-on files in the deploy clone step.
const (
	AppSourceGit      = "git"
	AppSourceTemplate = "template"
	// AppSourceImage deploys a prebuilt image — no git clone, no commit. The
	// deploy clone step makes an empty work dir and the image adapter writes a
	// generated compose; git_url/git_branch are empty (like a template app).
	AppSourceImage = "image"
)

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
	// MemLimitMB is the per-app hard memory ceiling in mebibytes (plan 06).
	// nil = unlimited / box default; wired into the deploy as a compose
	// mem_limit and SUMmed for the box-budget panel.
	MemLimitMB *int
	// DiskLimitMB is the per-app soft disk budget in mebibytes. nil = no limit
	// = no storage alert. Soft only — monitored + alerted by diskusage.Collector,
	// never enforced as a filesystem quota.
	DiskLimitMB *int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Source is "git" (default) or "template"; TemplateID is the add-on template
	// id for template-sourced apps (Track D / D3).
	Source     string
	TemplateID *string
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
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
	`, name, slug, gitURL, gitBranch, composeFile, buildKind, buildConfig).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID,
	)
	if isUniqueViolation(err) {
		return App{}, ErrConflict
	}
	return a, err
}

// CreateTemplateApp creates a template-sourced app (Track D / D3 add-on install).
// build_kind is "compose" — the materialized template ships a compose.yaml — and
// git_url is empty (the clone step materializes embedded files instead).
func (s *Store) CreateTemplateApp(ctx context.Context, name, slug, templateID, composeFile string) (App, error) {
	var a App
	err := s.pool.QueryRow(ctx, `
		INSERT INTO apps (name, slug, git_url, git_branch, compose_file, build_kind, build_config, source, template_id)
		VALUES ($1, $2, '', '', $3, 'compose', '{}', 'template', $4)
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
	`, name, slug, composeFile, templateID).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID,
	)
	if isUniqueViolation(err) {
		return App{}, ErrConflict
	}
	return a, err
}

// CreateImageApp creates an image-sourced app (deploy from a prebuilt image).
// source is 'image' and build_kind is 'image'; git_url/git_branch are empty (the
// deploy makes an empty work dir instead of cloning). compose_file is empty too
// — the image adapter generates the compose every deploy. The image ref + port
// live in buildConfig; private-registry creds are sealed separately.
func (s *Store) CreateImageApp(ctx context.Context, name, slug string, buildConfig json.RawMessage) (App, error) {
	if len(buildConfig) == 0 {
		buildConfig = json.RawMessage("{}")
	}
	var a App
	err := s.pool.QueryRow(ctx, `
		INSERT INTO apps (name, slug, git_url, git_branch, compose_file, build_kind, build_config, source)
		VALUES ($1, $2, '', '', '', 'image', $3, 'image')
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
	`, name, slug, buildConfig).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID,
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
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
		FROM apps WHERE slug = $1
	`, slug).Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

func (s *Store) GetApp(ctx context.Context, id string) (App, error) {
	var a App
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
		FROM apps WHERE id = $1
	`, id).Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

func (s *Store) ListApps(ctx context.Context) ([]App, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
		FROM apps ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApp applies a partial patch: any of the fields that are nil stay as
// they were. Returns the row post-update. Slug is intentionally not patchable —
// once set it's a stable URL handle.
// memLimitMB / diskLimitMB semantics: nil leaves the limit unchanged; a non-nil
// pointer sets it, with 0 meaning "clear → unlimited" (NULLIF maps 0 to SQL NULL).
func (s *Store) UpdateApp(ctx context.Context, id string, name, gitURL, gitBranch, composeFile, buildKind *string, buildConfig json.RawMessage, memLimitMB, diskLimitMB *int) (App, error) {
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
			mem_limit_mb = CASE WHEN $8::int IS NULL THEN mem_limit_mb ELSE NULLIF($8::int, 0) END,
			disk_limit_mb = CASE WHEN $9::int IS NULL THEN disk_limit_mb ELSE NULLIF($9::int, 0) END,
			updated_at   = NOW()
		WHERE id = $1
		RETURNING id, name, slug, git_url, git_branch, compose_file, build_kind, build_config, status, mem_limit_mb, disk_limit_mb, created_at, updated_at, source, template_id
	`, id, name, gitURL, gitBranch, composeFile, buildKind, bc, memLimitMB, diskLimitMB).Scan(
		&a.ID, &a.Name, &a.Slug, &a.GitURL, &a.GitBranch, &a.ComposeFile, &a.BuildKind, &a.BuildConfig, &a.Status, &a.MemLimitMB, &a.DiskLimitMB, &a.CreatedAt, &a.UpdatedAt, &a.Source, &a.TemplateID,
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

// MemAllocation is the box-budget aggregate: how much RAM apps have explicitly
// reserved via per-app limits, and how many apps carry a limit (plan 06).
type MemAllocation struct {
	AllocatedMB   int64
	AppsWithLimit int
	AppsTotal     int
}

// SumAppMemLimits totals the per-app memory limits for the box-budget panel.
// Apps with no limit (NULL) contribute nothing to AllocatedMB but are counted
// in AppsTotal, so the UI can warn that unlimited apps aren't budgeted.
func (s *Store) SumAppMemLimits(ctx context.Context) (MemAllocation, error) {
	var m MemAllocation
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(mem_limit_mb), 0)::bigint,
		       COUNT(mem_limit_mb)::int,
		       COUNT(*)::int
		FROM apps
	`).Scan(&m.AllocatedMB, &m.AppsWithLimit, &m.AppsTotal)
	return m, err
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
