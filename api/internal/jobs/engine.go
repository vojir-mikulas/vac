// Package jobs is VAC's user-facing cron: it runs an operator-defined command
// on a schedule inside one of an app's running service containers, records each
// run with a bounded output tail, and alerts on failure. It is modelled
// directly on the backup subsystem (backup/scheduler.go + backup/engine.go) —
// the same single sleeping-goroutine scheduler and the same running→terminal
// run lifecycle — with the backup destination removed: instead of shipping
// stdout somewhere, the tail of it is kept on the run row. See
// docs/plans/scheduled-jobs.md.
package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// outputCap bounds how much of a job's combined stdout/stderr is kept on the
// run row — enough to debug, cheap to store, pruned with the run. The tail (the
// last outputCap bytes) is what's retained, since errors usually surface at the
// end of the output.
const outputCap = 16 * 1024

// ExecRunner is the `docker exec` capture primitive (dockercli.Compose.Exec).
// Defined as an interface so the engine can be tested with a fake.
type ExecRunner interface {
	Exec(ctx context.Context, containerID string, cmd []string, out io.Writer) error
}

// EngineStore is the slice of *store.Store the engine depends on.
type EngineStore interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	GetService(ctx context.Context, appID, name string) (store.Service, error)
	CreateJobRun(ctx context.Context, jobID string) (store.JobRun, error)
	FinishJobRun(ctx context.Context, runID, status string, exitCode *int, output, errMsg *string) error
	UpdateJobSchedule(ctx context.Context, jobID string, lastRun, nextRun time.Time) error
}

// Notifier fires the job-failed event (notify.Dispatcher.JobFailed). May be nil.
type Notifier interface {
	JobFailed(appName, appID, jobName, errMsg string)
}

// Engine runs a single job end-to-end: exec the command in the running
// container under a hard timeout, capture a bounded output tail, record the run,
// roll the schedule forward, and notify on failure.
type Engine struct {
	store    EngineStore
	exec     ExecRunner
	notifier Notifier
	logger   *slog.Logger
	now      func() time.Time
}

// NewEngine wires the engine. notifier may be nil (failures still recorded).
func NewEngine(s EngineStore, exec ExecRunner, notifier Notifier, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:    s,
		exec:     exec,
		notifier: notifier,
		logger:   logger,
		now:      time.Now,
	}
}

// RunOnce executes one run of job. It records a job_runs row (running →
// success/failed/timeout), rolls last_run/next_run forward, and fires JobFailed
// on a non-success. The returned error mirrors the recorded failure (nil on
// success).
func (e *Engine) RunOnce(ctx context.Context, job store.ScheduledJob) error {
	start := e.now()
	app, err := e.store.GetApp(ctx, job.AppID)
	if err != nil {
		return fmt.Errorf("jobs: load app: %w", err)
	}

	run, err := e.store.CreateJobRun(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("jobs: open run: %w", err)
	}
	// Whatever the outcome, advance the denormalized schedule so the UI's
	// "last/next run" stays honest. Anchored on the run's start so an interval
	// job's cadence doesn't drift by however long the command took.
	defer e.rollSchedule(ctx, job, start)

	containerID, err := resolveContainer(ctx, e.store, job)
	if err != nil {
		return e.fail(ctx, run.ID, app, job, "failed", nil, "", err)
	}

	// Hard per-run cap so a hung command can't pin a container forever.
	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	buf := &cappedBuffer{cap: outputCap}
	execErr := e.exec.Exec(runCtx, containerID, []string{job.Command}, buf)
	output := buf.String()

	if execErr != nil {
		// A deadline hit means the timeout fired (the command was killed); record
		// it distinctly from an ordinary non-zero exit so the operator can tell a
		// hung job from a failing one.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			msg := fmt.Sprintf("job exceeded its %s timeout", timeout)
			return e.fail(ctx, run.ID, app, job, "timeout", nil, output, errors.New(msg))
		}
		return e.fail(ctx, run.ID, app, job, "failed", exitCodeOf(execErr), output, fmt.Errorf("job command failed: %w", execErr))
	}

	out := outputPtr(output)
	if err := e.store.FinishJobRun(ctx, run.ID, "success", intPtr(0), out, nil); err != nil {
		e.logger.Warn("jobs: record success", "job", job.ID, "err", err)
	}
	e.logger.Info("jobs: completed", "app", app.Slug, "job", job.Name, "service", job.ServiceName)
	return nil
}

// rollSchedule advances last_run (the run's start) and next_run (the next
// occurrence after start, anchored on this run) on the job row. Best-effort: a
// write failure just leaves the denormalized columns stale until the next run.
func (e *Engine) rollSchedule(ctx context.Context, job store.ScheduledJob, start time.Time) {
	job.LastRun = &start
	next := nextOccurrence(start, job)
	if err := e.store.UpdateJobSchedule(ctx, job.ID, start, next); err != nil {
		e.logger.Warn("jobs: roll schedule", "job", job.ID, "err", err)
	}
}

// resolveContainer finds the container to exec the command in: the service row's
// container_id, erroring if the service isn't running. By design a stopped
// service fails the run fast ("no running container") rather than starting the
// stack — the history row tells the operator why.
func resolveContainer(ctx context.Context, s EngineStore, job store.ScheduledJob) (string, error) {
	svc, err := s.GetService(ctx, job.AppID, job.ServiceName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("service %s no longer exists on this app", job.ServiceName)
		}
		return "", fmt.Errorf("jobs: load service %s: %w", job.ServiceName, err)
	}
	if svc.ContainerID == nil || *svc.ContainerID == "" {
		return "", fmt.Errorf("service %s has no running container", job.ServiceName)
	}
	return *svc.ContainerID, nil
}

// fail records the run in a non-success terminal state, fires the notification,
// and returns the cause.
func (e *Engine) fail(ctx context.Context, runID string, app store.App, job store.ScheduledJob, status string, exitCode *int, output string, cause error) error {
	msg := cause.Error()
	if err := e.store.FinishJobRun(ctx, runID, status, exitCode, outputPtr(output), &msg); err != nil {
		e.logger.Warn("jobs: record failure", "job", job.ID, "err", err)
	}
	if e.notifier != nil {
		e.notifier.JobFailed(app.Name, app.ID, job.Name, msg)
	}
	e.logger.Warn("jobs: failed", "app", app.Slug, "job", job.Name, "status", status, "err", msg)
	return cause
}

// exitCodeOf extracts the process exit code from an exec failure, if the error
// chain carries one (dockercli wraps the *exec.ExitError with %w).
func exitCodeOf(err error) *int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return intPtr(exitErr.ExitCode())
	}
	return nil
}

func intPtr(n int) *int { return &n }

// outputPtr returns nil for empty output so the column stays NULL rather than an
// empty string.
func outputPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// cappedBuffer is an io.Writer that retains only the last `cap` bytes written —
// the tail of a job's output. Older bytes are dropped as new ones arrive, so a
// chatty command can't balloon memory or the stored run row.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.cap > 0 && len(p) > b.cap {
		// This single write alone overflows the cap — keep only its tail.
		p = p[len(p)-b.cap:]
		b.buf.Reset()
	}
	b.buf.Write(p)
	if b.cap > 0 && b.buf.Len() > b.cap {
		// Trim the oldest bytes down to the cap.
		excess := b.buf.Len() - b.cap
		b.buf.Next(excess)
	}
	return n, nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
