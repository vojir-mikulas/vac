package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeploymentLogStream values. Kept as constants because the writer side picks
// them, not the user, so a typo at a call site is the bug we care about.
const (
	DeploymentLogStreamStdout = "stdout"
	DeploymentLogStreamStderr = "stderr"
	DeploymentLogStreamSystem = "system"
)

type DeploymentLog struct {
	ID           int64
	DeploymentID string
	ServiceName  *string
	Stream       string
	Message      string
	Timestamp    time.Time
}

// DeploymentLogRow is the input shape for batched inserts.
type DeploymentLogRow struct {
	ServiceName *string
	Stream      string
	Message     string
}

// AppendDeploymentLogs does one multi-row INSERT per call. The deploy log
// writer batches into chunks of ~200 lines or ~250ms to keep insert volume
// down — empty slices are a no-op.
func (s *Store) AppendDeploymentLogs(ctx context.Context, deploymentID string, rows []DeploymentLogRow) error {
	if len(rows) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO deployment_logs (deployment_id, service_name, stream, message) VALUES `)
	args := make([]any, 0, 1+3*len(rows))
	args = append(args, deploymentID)
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

// ListDeploymentLogs returns build-log rows in insertion order. `afterID` is
// the cursor — pass 0 to start from the beginning. Cap at `limit`.
func (s *Store) ListDeploymentLogs(ctx context.Context, deploymentID string, afterID int64, limit int) ([]DeploymentLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, service_name, stream, message, ts
		FROM deployment_logs
		WHERE deployment_id = $1 AND id > $2
		ORDER BY id
		LIMIT $3
	`, deploymentID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeploymentLog
	for rows.Next() {
		var l DeploymentLog
		if err := rows.Scan(&l.ID, &l.DeploymentID, &l.ServiceName, &l.Stream, &l.Message, &l.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CountDeploymentLogs is used by tests and the UI summary to assert the
// batched writer flushed.
func (s *Store) CountDeploymentLogs(ctx context.Context, deploymentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployment_logs WHERE deployment_id = $1`, deploymentID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
