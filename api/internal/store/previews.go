package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreatePreviewApp inserts a preview app derived from parent and copies the
// parent's non-sensitive env vars into it — atomically, so a partial failure
// leaves no half-built preview. The preview is an ordinary app row with
// is_preview=true and parent_app_id set; it overrides git_branch and inherits
// git_url / compose_file / build config from the parent. Slug is derived and
// passed in by the caller (the preview package) so collisions surface as
// ErrConflict here.
//
// Deliberately NOT copied (docs/plans/preview-deployments.md decision #4):
//   - sensitive=true env vars — a preview gets a clean slate for secrets.
//   - the parent's managed-DB DATABASE_URL — it is injected with sensitive=true,
//     so the sensitive filter excludes it; a preview must never read/write the
//     parent's production data.
//
// Managed databases (managed_databases.app_id) are likewise not linked — if a
// preview needs a DB the operator provisions a throwaway one on the preview app.
func (s *Store) CreatePreviewApp(ctx context.Context, parent App, slug, branch string) (App, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var bc any
	if len(parent.BuildConfig) > 0 {
		bc = parent.BuildConfig
	}
	var a App
	err = scanApp(tx.QueryRow(ctx, `
		INSERT INTO apps (name, slug, git_url, git_branch, compose_file, build_kind,
			build_config, source, template_id, is_preview, parent_app_id, last_preview_push_at)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, '{}'::jsonb), $8, $9, TRUE, $10, NOW())
		RETURNING `+appColumns,
		parent.Name+" ("+branch+")", slug, parent.GitURL, branch, parent.ComposeFile,
		parent.BuildKind, bc, parent.Source, parent.TemplateID, parent.ID), &a)
	if isUniqueViolation(err) {
		return App{}, ErrConflict
	}
	if err != nil {
		return App{}, err
	}

	// Copy only non-sensitive env vars; values are sealed ciphertext that we move
	// verbatim (no decrypt needed). write_only implies sensitive, so the
	// sensitive=false filter already excludes write-only secrets.
	if _, err := tx.Exec(ctx, `
		INSERT INTO env_vars (app_id, key, value, sensitive, write_only)
		SELECT $1, key, value, sensitive, write_only
		FROM env_vars WHERE app_id = $2 AND sensitive = FALSE
	`, a.ID, parent.ID); err != nil {
		return App{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return App{}, err
	}
	return a, nil
}

// GetPreviewByParentAndBranch finds an existing preview for (parent, branch).
// (parent_app_id, git_branch) is the stable identity the create-or-redeploy path
// keys off, independent of how the display slug was derived. Returns ErrNotFound
// when no preview exists yet.
func (s *Store) GetPreviewByParentAndBranch(ctx context.Context, parentID, branch string) (App, error) {
	var a App
	err := scanApp(s.pool.QueryRow(ctx, `
		SELECT `+appColumns+`
		FROM apps WHERE parent_app_id = $1 AND git_branch = $2 AND is_preview = TRUE
	`, parentID, branch), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return a, err
}

// ListPreviewsForApp returns a parent app's previews, newest first.
func (s *Store) ListPreviewsForApp(ctx context.Context, parentID string) ([]App, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+appColumns+`
		FROM apps WHERE parent_app_id = $1 AND is_preview = TRUE
		ORDER BY created_at DESC
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		if err := scanApp(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPreviews returns the number of preview apps across the whole instance —
// the global figure the cap (VAC_MAX_PREVIEWS) is checked against.
func (s *Store) CountPreviews(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM apps WHERE is_preview = TRUE`).Scan(&n)
	return n, err
}

// ListExpiredPreviews returns previews whose last push is older than olderThan —
// the TTL expirer's reap list. A preview with a NULL last_preview_push_at (never
// stamped) is never reaped here; CreatePreviewApp always stamps it.
func (s *Store) ListExpiredPreviews(ctx context.Context, olderThan time.Duration) ([]App, error) {
	cutoff := time.Now().Add(-olderThan)
	rows, err := s.pool.Query(ctx, `
		SELECT `+appColumns+`
		FROM apps
		WHERE is_preview = TRUE AND last_preview_push_at IS NOT NULL AND last_preview_push_at < $1
		ORDER BY last_preview_push_at ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		if err := scanApp(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TouchPreviewPush stamps a preview's last_preview_push_at to now — called on
// each push that redeploys an existing preview so the TTL clock resets.
func (s *Store) TouchPreviewPush(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE apps SET last_preview_push_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
