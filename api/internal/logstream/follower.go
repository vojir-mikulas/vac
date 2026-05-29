// Package logstream captures container stdout/stderr into runtime_logs and tees
// it to live WebSocket subscribers. A Supervisor keeps exactly one Follower per
// running container, reconciling against container churn (deploys, restarts,
// crashes) so logs survive a redeploy without re-ingesting history.
package logstream

import (
	"context"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// LogSource follows a container's logs. *dockercli.Compose satisfies it.
type LogSource interface {
	Logs(ctx context.Context, containerID string, since time.Time) (<-chan dockercli.LogLine, error)
}

// Sink persists runtime logs and trims the per-service ring buffer.
type Sink interface {
	AppendRuntimeLogs(ctx context.Context, appID string, rows []store.RuntimeLogRow) ([]int64, error)
	TrimRuntimeLogsToRingBuffer(ctx context.Context, appID, serviceName string, keepN int) (int64, error)
}

// Publisher tees frames to the hub. *ws.Hub satisfies it.
type Publisher interface {
	Publish(topic string, msg []byte)
}

// runtimeLogData is the Data of a "log" frame.
type runtimeLogData struct {
	Stream  string `json:"stream"`
	Message string `json:"message"`
}

// follower captures one container's logs until its context is cancelled.
type follower struct {
	src        LogSource
	sink       Sink
	pub        Publisher
	appID      string
	service    string
	container  string
	ringBuffer int
	flushEvery time.Duration
	trimEvery  time.Duration
	maxBatch   int
	logger     *slog.Logger
}

// run streams, batches, persists, and publishes until ctx is done or the log
// stream ends. A final flush captures anything buffered at teardown.
func (f *follower) run(ctx context.Context) {
	since := time.Now()
	lines, err := f.src.Logs(ctx, f.container, since)
	if err != nil {
		f.logger.Warn("logstream: follow failed", "service", f.service, "container", short(f.container), "err", err)
		return
	}

	flush := time.NewTicker(f.flushEvery)
	defer flush.Stop()
	trim := time.NewTicker(f.trimEvery)
	defer trim.Stop()

	var buf []store.RuntimeLogRow
	doFlush := func() {
		if len(buf) == 0 {
			return
		}
		rows := buf
		buf = nil
		// Use a background context for the final flush so a cancelled ctx
		// still persists the tail.
		wctx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			wctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
		}
		ids, err := f.sink.AppendRuntimeLogs(wctx, f.appID, rows)
		if err != nil {
			f.logger.Warn("logstream: append failed", "service", f.service, "err", err)
			return
		}
		f.publish(rows, ids)
	}

	for {
		select {
		case <-ctx.Done():
			doFlush()
			return
		case line, ok := <-lines:
			if !ok {
				doFlush()
				return
			}
			buf = append(buf, store.RuntimeLogRow{
				ServiceName: f.service,
				Stream:      line.Stream,
				Message:     line.Message,
			})
			if len(buf) >= f.maxBatch {
				doFlush()
			}
		case <-flush.C:
			doFlush()
		case <-trim.C:
			if _, err := f.sink.TrimRuntimeLogsToRingBuffer(ctx, f.appID, f.service, f.ringBuffer); err != nil {
				f.logger.Debug("logstream: trim failed", "service", f.service, "err", err)
			}
		}
	}
}

func (f *follower) publish(rows []store.RuntimeLogRow, ids []int64) {
	if f.pub == nil {
		return
	}
	topic := ws.LogsTopic(f.appID)
	now := time.Now()
	n := len(ids)
	if len(rows) < n {
		n = len(rows)
	}
	for i, row := range rows[:n] {
		frame, err := ws.LogFrame(ws.TypeLog, f.service, ids[:n][i], now, runtimeLogData{
			Stream:  row.Stream,
			Message: row.Message,
		})
		if err != nil {
			continue
		}
		f.pub.Publish(topic, frame)
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
