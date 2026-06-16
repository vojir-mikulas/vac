package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Actor types recorded in audit_log.actor_type. Kept here (not a DB enum) so
// the set is one source of truth alongside the other Go-side status enums.
const (
	ActorUser      = "user"      // a logged-in operator (session cookie)
	ActorAPIToken  = "api_token" // a CLI / automation bearer token
	ActorSystem    = "system"    // VAC itself (auto-deploy, crashloop restart)
	ActorAnonymous = "anonymous" // unauthenticated (a failed login attempt)
)

// AuditEntry is the write shape for one audit row. The non-pointer fields are
// always set by the middleware; the pointers are the handler's optional
// enrichment (see package audit). Metadata is raw JSON (nil = SQL NULL).
type AuditEntry struct {
	ActorUserID *string
	ActorType   string
	Action      string
	TargetType  *string
	TargetID    *string
	Summary     *string
	Metadata    json.RawMessage
	IP          *string
	UserAgent   *string
	StatusCode  int
	// Revertable marks the curated set of actions that carry a before-snapshot
	// in Metadata and can be undone (plan 11, Part 2).
	Revertable bool
}

// AuditLog is the read shape, including the server-assigned id and timestamp.
type AuditLog struct {
	ID          string
	ActorUserID *string
	ActorType   string
	Action      string
	TargetType  *string
	TargetID    *string
	Summary     *string
	Metadata    json.RawMessage
	IP          *string
	UserAgent   *string
	StatusCode  int
	Revertable  bool
	RevertedAt  *time.Time
	CreatedAt   time.Time
}

// InsertAuditLog appends one row. Failures here must never fail the audited
// request — callers log and move on.
func (s *Store) InsertAuditLog(ctx context.Context, e AuditEntry) error {
	var meta any
	if len(e.Metadata) > 0 {
		meta = []byte(e.Metadata)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log
			(actor_user_id, actor_type, action, target_type, target_id,
			 summary, metadata, ip, user_agent, status_code, revertable)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		e.ActorUserID, e.ActorType, e.Action, e.TargetType, e.TargetID,
		e.Summary, meta, e.IP, e.UserAgent, e.StatusCode, e.Revertable,
	)
	return err
}

// ListAuditLog returns the most recent entries, newest first. This is the
// successor to the derived activity feed; pagination lands with its UI. limit
// is clamped to a sane ceiling.
func (s *Store) ListAuditLog(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+auditColumns+`
		FROM audit_log
		-- Unauthenticated failures (probes, failed logins) are not operator
		-- activity; they live in security_events. New ones are diverted at write
		-- time, but this also hides any that predate that split.
		WHERE NOT (actor_type = '`+ActorAnonymous+`' AND status_code >= 400)
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditLog
	for rows.Next() {
		a, err := scanAuditLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const auditColumns = `id, actor_user_id, actor_type, action, target_type, target_id,
	summary, metadata, ip, user_agent, status_code, revertable, reverted_at, created_at`

func scanAuditLog(row pgx.Row) (AuditLog, error) {
	var a AuditLog
	err := row.Scan(
		&a.ID, &a.ActorUserID, &a.ActorType, &a.Action, &a.TargetType, &a.TargetID,
		&a.Summary, &a.Metadata, &a.IP, &a.UserAgent, &a.StatusCode, &a.Revertable, &a.RevertedAt, &a.CreatedAt,
	)
	return a, err
}

// GetAuditLog fetches one entry by id. Returns ErrNotFound when absent — the
// revert handler uses this to resolve the action + before-snapshot to undo.
func (s *Store) GetAuditLog(ctx context.Context, id string) (AuditLog, error) {
	a, err := scanAuditLog(s.pool.QueryRow(ctx, `SELECT `+auditColumns+` FROM audit_log WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuditLog{}, ErrNotFound
	}
	return a, err
}

// MarkAuditReverted stamps reverted_at on an entry so the UI shows it as undone
// and refuses a second revert. Only stamps a row that is revertable and not
// already reverted; RowsAffected==0 means it was already undone (caller maps to
// a 409 conflict).
func (s *Store) MarkAuditReverted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE audit_log SET reverted_at = NOW()
		WHERE id = $1 AND revertable AND reverted_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// DeleteAuditLogOlderThan prunes entries past the retention window. Wired into
// the nightly retention pruner on the activity_retention_days budget.
func (s *Store) DeleteAuditLogOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_log WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
