package store

import (
	"context"
	"time"
)

// SecurityEvent is one unauthenticated attempt against the control plane — a
// failed login or a probe to a path that doesn't exist. The audit middleware
// diverts these out of audit_log (which is the operator's own action log) and
// into security_events so the probing has a durable record of its own. There is
// no actor and nothing to revert: just method, the raw path hit, the outcome,
// and the source. IP / UserAgent are pointers (nil = SQL NULL) since a request
// may carry neither.
type SecurityEvent struct {
	ID         string
	Method     string
	Path       string
	StatusCode int
	IP         *string
	UserAgent  *string
	CreatedAt  time.Time
}

// InsertSecurityEvent appends one row. Like audit inserts, a failure here must
// never fail the request being recorded — callers log and move on.
func (s *Store) InsertSecurityEvent(ctx context.Context, e SecurityEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO security_events (method, path, status_code, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)
	`, e.Method, e.Path, e.StatusCode, e.IP, e.UserAgent)
	return err
}

// ListSecurityEvents returns the most recent attempts, newest first. limit is
// clamped to a sane ceiling (the UI groups these by source IP client-side).
func (s *Store) ListSecurityEvents(ctx context.Context, limit int) ([]SecurityEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, method, path, status_code, ip, user_agent, created_at
		FROM security_events
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecurityEvent
	for rows.Next() {
		var e SecurityEvent
		if err := rows.Scan(&e.ID, &e.Method, &e.Path, &e.StatusCode, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteSecurityEventsOlderThan prunes attempts past the retention window. Wired
// into the nightly retention pruner on the same activity_retention_days budget
// as the audit log.
func (s *Store) DeleteSecurityEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM security_events WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
