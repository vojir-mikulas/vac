package deploy

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// deploymentLogSink is the slice of *store.Store this file needs.
// Decoupled so pipeline tests can substitute a fake. AppendDeploymentLogs
// returns the generated row ids so the live stream can tag each frame.
type deploymentLogSink interface {
	AppendDeploymentLogs(ctx context.Context, deploymentID string, rows []store.DeploymentLogRow) ([]int64, error)
}

// Publisher is the slice of *ws.Hub the log writer publishes to. nil disables
// live streaming (tests, or a process without the hub wired).
type Publisher interface {
	Publish(topic string, msg []byte)
}

// buildLogPayload is the Data of a "build" frame.
type buildLogPayload struct {
	Stream      string  `json:"stream"`
	Message     string  `json:"message"`
	ServiceName *string `json:"service_name,omitempty"`
}

// LogWriter is an io.Writer that batches docker/build output into chunks
// of deployment_logs INSERTs. dockercli's runStreaming calls Write once per
// line (with the trailing newline included), so we strip the newline and
// buffer until we have `maxBatch` rows or someone calls Flush. After each
// persisted batch it also publishes one frame per row to build:{deploymentID}.
type LogWriter struct {
	sink         deploymentLogSink
	pub          Publisher
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
// `pub` may be nil to disable live publishing.
func NewLogWriter(ctx context.Context, sink deploymentLogSink, pub Publisher, deploymentID, stream string, serviceName *string) *LogWriter {
	return &LogWriter{
		sink:         sink,
		pub:          pub,
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
	ids, err := lw.sink.AppendDeploymentLogs(lw.ctx, lw.deploymentID, rows)
	if err != nil {
		return err
	}
	publishBuildRows(lw.pub, lw.deploymentID, rows, ids)
	return nil
}

// LogSystem is a one-shot pipeline-level message helper. Wraps the message
// in the "system" stream and flushes immediately so the row is visible to
// the UI (and live subscribers) without waiting for the next batch.
func LogSystem(ctx context.Context, sink deploymentLogSink, pub Publisher, deploymentID, msg string) error {
	rows := []store.DeploymentLogRow{{Stream: store.DeploymentLogStreamSystem, Message: msg}}
	ids, err := sink.AppendDeploymentLogs(ctx, deploymentID, rows)
	if err != nil {
		return err
	}
	publishBuildRows(pub, deploymentID, rows, ids)
	return nil
}

// publishBuildRows tees persisted rows to the live build topic, one frame each.
// ids and rows are index-aligned (both produced by the same INSERT); if they
// disagree (a partial scan) we publish only the rows we have ids for.
func publishBuildRows(pub Publisher, deploymentID string, rows []store.DeploymentLogRow, ids []int64) {
	if pub == nil {
		return
	}
	topic := ws.BuildTopic(deploymentID)
	now := time.Now()
	n := len(ids)
	if len(rows) < n {
		n = len(rows)
	}
	for i, row := range rows[:n] {
		frame, err := ws.LogFrame(ws.TypeBuild, "", ids[:n][i], now, buildLogPayload{
			Stream:      row.Stream,
			Message:     row.Message,
			ServiceName: row.ServiceName,
		})
		if err != nil {
			continue
		}
		pub.Publish(topic, frame)
	}
}

// PublishBuildEnd emits the terminator frame that tells live subscribers the
// build stream is finished. Called by the pipeline once a deployment settles.
func PublishBuildEnd(pub Publisher, deploymentID string) {
	if pub == nil {
		return
	}
	frame, err := ws.Control(ws.TypeBuildEnd, time.Now())
	if err != nil {
		return
	}
	pub.Publish(ws.BuildTopic(deploymentID), frame)
}

// PublishDeploymentsChanged signals the instance-wide deploy-queue topic that
// something changed (a deployment was created, transitioned, or settled). It
// carries no payload — the queue-panel WS handler re-reads the active list on
// each frame and pushes a fresh snapshot. Cheap and nil-safe.
func PublishDeploymentsChanged(pub Publisher) {
	if pub == nil {
		return
	}
	frame, err := ws.Control(ws.TypeDeployments, time.Now())
	if err != nil {
		return
	}
	pub.Publish(ws.DeploymentsTopic, frame)
}
