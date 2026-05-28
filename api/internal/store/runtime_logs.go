package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	RuntimeLogStreamStdout = "stdout"
	RuntimeLogStreamStderr = "stderr"
	RuntimeLogStreamSystem = "system"
)

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
// Multi-row INSERT, empty slices are a no-op.
func (s *Store) AppendRuntimeLogs(ctx context.Context, appID string, rows []RuntimeLogRow) error {
	if len(rows) == 0 {
		return nil
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
	_, err := s.pool.Exec(ctx, b.String(), args...)
	return err
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

// CountRuntimeLogs is a test helper / UI summary.
func (s *Store) CountRuntimeLogs(ctx context.Context, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM runtime_logs WHERE app_id = $1`, appID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
