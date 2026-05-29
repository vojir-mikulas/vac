package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Runtime log stream tags.
const (
	RuntimeLogStreamStdout = "stdout"
	RuntimeLogStreamStderr = "stderr"
	RuntimeLogStreamSystem = "system"
)

// RuntimeLog is one persisted container log line.
type RuntimeLog struct {
	ID          int64
	AppID       string
	ServiceName string
	Stream      string
	Message     string
	Timestamp   time.Time
}

// RuntimeLogRow is the input shape for batched inserts.
type RuntimeLogRow struct {
	ServiceName string
	Stream      string
	Message     string
}

// AppendRuntimeLogs is the runtime-log counterpart of AppendDeploymentLogs.
// Multi-row INSERT returning the generated ids in order; empty slices are a
// no-op. The ids let the live WS stream tag each frame for client-side dedup.
func (s *Store) AppendRuntimeLogs(ctx context.Context, appID string, rows []RuntimeLogRow) ([]int64, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO runtime_logs (app_id, service_name, stream, message) VALUES `)
	args := make([]any, 0, 1+3*len(rows))
	args = append(args, appID)
	for i, r := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		base := 2 + i*3
		b.WriteString("($1,$")
		b.WriteString(strconv.Itoa(base))
		b.WriteString(",$")
		b.WriteString(strconv.Itoa(base + 1))
		b.WriteString(",$")
		b.WriteString(strconv.Itoa(base + 2))
		b.WriteByte(')')
		args = append(args, r.ServiceName, r.Stream, r.Message)
	}
	b.WriteString(" RETURNING id")
	dbRows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()
	ids := make([]int64, 0, len(rows))
	for dbRows.Next() {
		var id int64
		if err := dbRows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, dbRows.Err()
}

// ListRuntimeLogs returns runtime-log rows newest-first. `beforeID` is a
// cursor — pass 0 to start from the newest. Optional `serviceName` filter.
func (s *Store) ListRuntimeLogs(ctx context.Context, appID, serviceName string, beforeID int64, limit int) ([]RuntimeLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	// beforeID == 0 means "no cursor"; use a giant sentinel so the WHERE
	// clause stays uniform.
	if beforeID == 0 {
		beforeID = int64(1<<63 - 1)
	}
	var rows pgx.Rows
	var err error
	if serviceName == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, app_id, service_name, stream, message, ts
			FROM runtime_logs
			WHERE app_id = $1 AND id < $2
			ORDER BY id DESC
			LIMIT $3
		`, appID, beforeID, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, app_id, service_name, stream, message, ts
			FROM runtime_logs
			WHERE app_id = $1 AND service_name = $2 AND id < $3
			ORDER BY id DESC
			LIMIT $4
		`, appID, serviceName, beforeID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeLog
	for rows.Next() {
		var l RuntimeLog
		if err := rows.Scan(&l.ID, &l.AppID, &l.ServiceName, &l.Stream, &l.Message, &l.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteRuntimeLogsOlderThan is the prune call used by the retention
// goroutine. Returns the number of rows deleted.
func (s *Store) DeleteRuntimeLogsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM runtime_logs WHERE ts < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// TrimRuntimeLogsToRingBuffer enforces the per-service ring buffer from
// mvp.md § Log Retention: it keeps only the newest keepN rows for one
// (app, service) and deletes the overflow. Returns the number of rows deleted.
// Called periodically by the log follower and nightly by the retention pruner.
func (s *Store) TrimRuntimeLogsToRingBuffer(ctx context.Context, appID, serviceName string, keepN int) (int64, error) {
	if keepN <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM runtime_logs
		WHERE id IN (
			SELECT id FROM (
				SELECT id, row_number() OVER (ORDER BY id DESC) AS rn
				FROM runtime_logs
				WHERE app_id = $1 AND service_name = $2
			) t WHERE t.rn > $3
		)
	`, appID, serviceName, keepN)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListRuntimeLogServices returns the distinct (app_id, service_name) pairs that
// currently have runtime logs — the nightly pruner iterates these to apply the
// ring-buffer trim per service.
func (s *Store) ListRuntimeLogServices(ctx context.Context) ([]struct{ AppID, ServiceName string }, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT app_id, service_name FROM runtime_logs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ AppID, ServiceName string }
	for rows.Next() {
		var p struct{ AppID, ServiceName string }
		if err := rows.Scan(&p.AppID, &p.ServiceName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountRuntimeLogs is a test helper / UI summary.
func (s *Store) CountRuntimeLogs(ctx context.Context, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM runtime_logs WHERE app_id = $1`, appID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
