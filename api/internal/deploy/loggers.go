package deploy

import (
	"bytes"
	"context"
	"sync"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// deploymentLogSink is the slice of *store.Store this file needs.
// Decoupled so pipeline tests can substitute a fake.
type deploymentLogSink interface {
	AppendDeploymentLogs(ctx context.Context, deploymentID string, rows []store.DeploymentLogRow) error
}

// LogWriter is an io.Writer that batches docker/build output into chunks
// of deployment_logs INSERTs. dockercli's runStreaming calls Write once per
// line (with the trailing newline included), so we strip the newline and
// buffer until we have `maxBatch` rows or someone calls Flush.
type LogWriter struct {
	sink         deploymentLogSink
	ctx          context.Context
	deploymentID string
	serviceName  *string
	stream       string
	maxBatch     int

	mu  sync.Mutex
	buf []store.DeploymentLogRow
}

// NewLogWriter returns a writer scoped to one deployment + stream tag.
// `serviceName` is nil for pipeline-level lines, set for build-stage output.
func NewLogWriter(ctx context.Context, sink deploymentLogSink, deploymentID, stream string, serviceName *string) *LogWriter {
	return &LogWriter{
		sink:         sink,
		ctx:          ctx,
		deploymentID: deploymentID,
		serviceName:  serviceName,
		stream:       stream,
		maxBatch:     200,
	}
}

// Write splits p on newlines and accumulates one row per non-empty line.
// Flushes when the buffer reaches maxBatch.
func (lw *LogWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	for _, line := range bytes.Split(p, []byte{'\n'}) {
		s := string(bytes.TrimRight(line, "\r"))
		if s == "" {
			continue
		}
		lw.buf = append(lw.buf, store.DeploymentLogRow{
			ServiceName: lw.serviceName,
			Stream:      lw.stream,
			Message:     s,
		})
	}
	if len(lw.buf) >= lw.maxBatch {
		if err := lw.flushLocked(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes any buffered rows immediately. Always called by the pipeline
// at step boundaries and at end of run.
func (lw *LogWriter) Flush() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.flushLocked()
}

func (lw *LogWriter) flushLocked() error {
	if len(lw.buf) == 0 {
		return nil
	}
	rows := lw.buf
	lw.buf = nil
	return lw.sink.AppendDeploymentLogs(lw.ctx, lw.deploymentID, rows)
}

// LogSystem is a one-shot pipeline-level message helper. Wraps the message
// in the "system" stream and flushes immediately so the row is visible to
// the UI without waiting for the next batch.
func LogSystem(ctx context.Context, sink deploymentLogSink, deploymentID, msg string) error {
	return sink.AppendDeploymentLogs(ctx, deploymentID, []store.DeploymentLogRow{
		{Stream: store.DeploymentLogStreamSystem, Message: msg},
	})
}
