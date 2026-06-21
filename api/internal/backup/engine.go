package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ExecRunner is the `docker exec` capture primitive (dockercli.Compose.Exec).
// Defined as an interface so the engine can be tested with a fake.
type ExecRunner interface {
	Exec(ctx context.Context, containerID string, cmd []string, out io.Writer) error
}

// EngineStore is the slice of *store.Store the engine depends on.
type EngineStore interface {
	GetApp(ctx context.Context, id string) (store.App, error)
	GetService(ctx context.Context, appID, name string) (store.Service, error)
	CreateBackupRun(ctx context.Context, configID string) (store.BackupRun, error)
	FinishBackupRun(ctx context.Context, runID, status string, sizeBytes *int64, artifactKey *string, errMsg *string) error
	PruneBackupRuns(ctx context.Context, configID string, keep int) (int64, error)
}

// Notifier fires the backup-failed event (notify.Dispatcher.BackupFailed).
type Notifier interface {
	BackupFailed(appName, appID, service, errMsg string)
}

// Engine runs a single backup end-to-end: exec the command in the running
// container, stream stdout to the destination, record the run, prune, and notify
// on failure.
type Engine struct {
	store    EngineStore
	exec     ExecRunner
	box      *crypto.Box
	workDir  string
	notifier Notifier
	logger   *slog.Logger
	now      func() time.Time
}

// NewEngine wires the engine. notifier may be nil (failures still recorded).
func NewEngine(s EngineStore, exec ExecRunner, box *crypto.Box, workDir string, notifier Notifier, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:    s,
		exec:     exec,
		box:      box,
		workDir:  workDir,
		notifier: notifier,
		logger:   logger,
		now:      time.Now,
	}
}

// defaultBackupTimeout bounds a single backup run (command exec + upload). The
// scheduler runs configs serially, so without a cap one hung pg_dump or a
// stalled S3 PUT would block every other config's backup indefinitely.
const defaultBackupTimeout = 30 * time.Minute

// RunOnce executes one backup for cfg. It records a backup_runs row (running →
// success/failed), prunes old artifacts on success, and fires BackupFailed on
// failure. The returned error mirrors the recorded failure (nil on success).
func (e *Engine) RunOnce(ctx context.Context, cfg store.BackupConfig) error {
	app, err := e.store.GetApp(ctx, cfg.AppID)
	if err != nil {
		return fmt.Errorf("backup: load app: %w", err)
	}

	run, err := e.store.CreateBackupRun(ctx, cfg.ID)
	if err != nil {
		return fmt.Errorf("backup: open run: %w", err)
	}

	containerID, err := resolveContainer(ctx, e.store, cfg)
	if err != nil {
		return e.fail(ctx, run.ID, app, cfg, err)
	}

	dest, err := NewDestination(cfg, e.box, e.workDir)
	if err != nil {
		return e.fail(ctx, run.ID, app, cfg, err)
	}

	key := artifactKey(app.Slug, cfg.ServiceName, run.ID, e.now())

	// Hard per-run cap covering the command exec and the upload. On expiry the
	// exec is killed and Put unblocks, surfacing as the run's failure.
	runCtx, cancel := context.WithTimeout(ctx, defaultBackupTimeout)
	defer cancel()

	// Bridge the exec writer to the destination reader with a pipe so nothing is
	// buffered in full. A non-zero exit closes the pipe with that error, which
	// surfaces through Put.
	pr, pw := io.Pipe()
	execErrCh := make(chan error, 1)
	go func() {
		execErr := e.exec.Exec(runCtx, containerID, []string{cfg.Command}, pw)
		_ = pw.CloseWithError(execErr)
		execErrCh <- execErr
	}()

	size, putErr := dest.Put(runCtx, key, pr)
	// Drain anything Put left unread so the exec goroutine can't block, then wait
	// for its exit status.
	_, _ = io.Copy(io.Discard, pr)
	_ = pr.Close()
	execErr := <-execErrCh

	if execErr != nil {
		return e.fail(ctx, run.ID, app, cfg, fmt.Errorf("backup command failed: %w", execErr))
	}
	if putErr != nil {
		return e.fail(ctx, run.ID, app, cfg, fmt.Errorf("upload to %s failed: %w", cfg.Destination, putErr))
	}

	// Settle the row on a detached context: the success bytes are already durably
	// stored, so a shutdown cancelling ctx here must not skip the terminal write
	// and strand the row in `running`.
	finCtx, finCancel := detached(ctx, 10*time.Second)
	defer finCancel()
	if err := e.store.FinishBackupRun(finCtx, run.ID, "success", &size, &key, nil); err != nil {
		e.logger.Warn("backup: record success", "config", cfg.ID, "err", err)
	}
	if err := dest.Prune(ctx, prunePrefix(app.Slug, cfg.ServiceName), cfg.KeepCount); err != nil {
		// Non-fatal: the artifact is safely stored; old ones just linger.
		e.logger.Warn("backup: prune", "config", cfg.ID, "err", err)
	} else if _, err := e.store.PruneBackupRuns(ctx, cfg.ID, cfg.KeepCount); err != nil {
		// Prune the run rows in lockstep so none outlive their artifact. Non-fatal:
		// a stale row at worst points the verifier at a missing object next cycle.
		e.logger.Warn("backup: prune runs", "config", cfg.ID, "err", err)
	}
	e.logger.Info("backup: completed", "app", app.Slug, "service", cfg.ServiceName, "bytes", size, "key", key)
	return nil
}

// serviceGetter is the one store method resolveContainer needs — shared by the
// dump Engine and the Restorer.
type serviceGetter interface {
	GetService(ctx context.Context, appID, name string) (store.Service, error)
}

// resolveContainer finds the container to exec the dump/restore in. A managed-DB
// backup carries an explicit ContainerName (the shared engine container, e.g.
// vac-db) and uses it directly. A normal app backup resolves the service row to
// its container_id. As a back-compat fallback (pre-container_name managed rows), a
// service-not-found treats ServiceName as a literal container name — docker exec
// accepts names as well as IDs.
func resolveContainer(ctx context.Context, s serviceGetter, cfg store.BackupConfig) (string, error) {
	if cfg.ContainerName != nil && *cfg.ContainerName != "" {
		return *cfg.ContainerName, nil
	}
	svc, err := s.GetService(ctx, cfg.AppID, cfg.ServiceName)
	if err == nil {
		if svc.ContainerID == nil || *svc.ContainerID == "" {
			return "", fmt.Errorf("service %s has no running container", cfg.ServiceName)
		}
		return *svc.ContainerID, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return cfg.ServiceName, nil
	}
	return "", fmt.Errorf("backup: load service %s: %w", cfg.ServiceName, err)
}

// detached returns a context carrying ctx's values but not its cancellation,
// bounded by d. Used for terminal DB writes that must complete even when the
// run's context was cancelled (shutdown, per-run timeout) — otherwise the row
// would hang in `running` until the boot reaper settles it.
func detached(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

// fail records the run as failed, fires the notification, and returns the error.
func (e *Engine) fail(ctx context.Context, runID string, app store.App, cfg store.BackupConfig, cause error) error {
	msg := cause.Error()
	finCtx, cancel := detached(ctx, 10*time.Second)
	defer cancel()
	if err := e.store.FinishBackupRun(finCtx, runID, "failed", nil, nil, &msg); err != nil {
		e.logger.Warn("backup: record failure", "config", cfg.ID, "err", err)
	}
	if e.notifier != nil {
		e.notifier.BackupFailed(app.Name, app.ID, cfg.ServiceName, msg)
	}
	e.logger.Warn("backup: failed", "app", app.Slug, "service", cfg.ServiceName, "err", msg)
	return cause
}

// artifactKey is the destination key for a dump:
// slug/service/<sortable-ts>-<runID>.dump. The timestamp leads so Prune's string
// sort stays chronological; the run ID suffix guarantees uniqueness — two runs of
// the same (app, service) in the same second (a manual run racing the scheduler,
// or a fast retry) would otherwise collide on an identical key and the second Put
// would overwrite the first, leaving a `success` row pointing at another run's
// bytes.
func artifactKey(slug, service, runID string, ts time.Time) string {
	return keyJoin(slug, service, ts.UTC().Format("20060102T150405Z")+"-"+runID+".dump")
}

// prunePrefix is the key prefix Prune scans for a given (app, service).
func prunePrefix(slug, service string) string {
	return keyJoin(slug, service) + "/"
}
