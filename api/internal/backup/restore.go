package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrRestoreUnsupported is returned when a config's backup command isn't a
// recognized engine default, so VAC can't infer the restore command
// (backup-restore decision #1 — download and restore manually instead).
var ErrRestoreUnsupported = errors.New("backup: restore unsupported for this command")

// ErrRestoreInProgress is returned when a restore for the same config is already
// running (decision #5 — one restore at a time per config).
var ErrRestoreInProgress = errors.New("backup: a restore is already running for this config")

// ExecStdinRunner is the `docker exec -i` primitive (dockercli.Compose.ExecStdin)
// that pipes the artifact into the engine CLI. An interface so the Restorer can
// be tested with a fake.
type ExecStdinRunner interface {
	ExecStdin(ctx context.Context, containerID string, cmd []string, stdin io.Reader) error
}

// RestoreCommandResolver maps a stored backup command to the command that
// replays its artifact. *dbprovision.Provisioner satisfies it.
type RestoreCommandResolver interface {
	RestoreCommandFor(backupCommand string) (restoreCommand string, ok bool)
}

// RestoreNotifier fires the restore-finished event. *notify.Dispatcher satisfies
// it (RestoreFinished).
type RestoreNotifier interface {
	RestoreFinished(appName, appID, service string, ok bool)
}

// RestorerStore is the persistence slice the Restorer depends on.
type RestorerStore interface {
	serviceGetter
	GetApp(ctx context.Context, id string) (store.App, error)
	GetBackupRun(ctx context.Context, runID string) (store.BackupRun, error)
	CreateRestoreRun(ctx context.Context, configID, sourceRunID string) (store.BackupRestore, error)
	FinishRestoreRun(ctx context.Context, restoreID, status string, errMsg *string) error
	LatestRestoreRun(ctx context.Context, configID string) (store.BackupRestore, error)
}

// Restorer replays a recorded backup run end-to-end: read the artifact back from
// its destination, resolve the engine restore command, and stream it into the
// target container over `docker exec -i`. The precise inverse of Engine.RunOnce,
// reusing resolveContainer / Destination.Open / the run-row lifecycle.
type Restorer struct {
	store    RestorerStore
	exec     ExecStdinRunner
	box      *crypto.Box
	workDir  string
	resolver RestoreCommandResolver
	notifier RestoreNotifier
	logger   *slog.Logger
}

// NewRestorer wires the restorer. notifier may be nil (failures still recorded).
func NewRestorer(s RestorerStore, exec ExecStdinRunner, box *crypto.Box, workDir string, resolver RestoreCommandResolver, notifier RestoreNotifier, logger *slog.Logger) *Restorer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Restorer{
		store:    s,
		exec:     exec,
		box:      box,
		workDir:  workDir,
		resolver: resolver,
		notifier: notifier,
		logger:   logger,
	}
}

// CanRestore reports whether cfg's backup command maps to a known restore command
// (decision #1). The handler checks this before accepting a restore so a custom
// command is refused up front rather than failing mid-stream.
func (rr *Restorer) CanRestore(cfg store.BackupConfig) bool {
	if rr.resolver == nil {
		return false
	}
	_, ok := rr.resolver.RestoreCommandFor(cfg.Command)
	return ok
}

// Restore replays sourceRunID's artifact back into cfg's target container. It
// records a backup_restores row (running → success/failed) and fires the notify
// event. The returned error mirrors the recorded failure (nil on success).
//
// Destructive and not atomic: psql/mariadb apply statements as they arrive, so a
// mid-stream failure leaves the database partially overwritten — there is no
// rollback. The caller gates this behind step-up 2FA + a typed confirmation.
func (rr *Restorer) Restore(ctx context.Context, cfg store.BackupConfig, sourceRunID string) error {
	app, err := rr.store.GetApp(ctx, cfg.AppID)
	if err != nil {
		return fmt.Errorf("backup: load app: %w", err)
	}

	restoreCmd, ok := rr.resolveCommand(cfg)
	if !ok {
		return ErrRestoreUnsupported
	}

	run, err := rr.store.GetBackupRun(ctx, sourceRunID)
	if err != nil {
		return fmt.Errorf("backup: load run: %w", err)
	}
	if run.ConfigID != cfg.ID {
		return fmt.Errorf("backup: run %s does not belong to this backup config", sourceRunID)
	}
	if run.Status != "success" || run.ArtifactKey == nil {
		return fmt.Errorf("backup: run %s has no restorable artifact", sourceRunID)
	}

	// One restore at a time per config (decision #5). The row is the guard; a
	// narrow race is acceptable for a single-operator box. Fail closed on any
	// lookup error other than "no prior run": a transient DB error must not be
	// read as "nothing running" and let a second destructive, non-atomic restore
	// proceed against the same database.
	switch latest, err := rr.store.LatestRestoreRun(ctx, cfg.ID); {
	case err == nil && latest.Status == "running":
		return ErrRestoreInProgress
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("backup: check for in-progress restore: %w", err)
	}

	rec, err := rr.store.CreateRestoreRun(ctx, cfg.ID, sourceRunID)
	if err != nil {
		return fmt.Errorf("backup: open restore run: %w", err)
	}

	container, err := resolveContainer(ctx, rr.store, cfg)
	if err != nil {
		return rr.fail(ctx, rec.ID, app, cfg, err)
	}

	dest, err := NewDestination(cfg, rr.box, rr.workDir)
	if err != nil {
		return rr.fail(ctx, rec.ID, app, cfg, err)
	}
	reader, err := dest.Open(ctx, *run.ArtifactKey)
	if err != nil {
		return rr.fail(ctx, rec.ID, app, cfg, fmt.Errorf("open artifact: %w", err))
	}
	defer func() { _ = reader.Close() }()

	if err := rr.exec.ExecStdin(ctx, container, []string{restoreCmd}, reader); err != nil {
		return rr.fail(ctx, rec.ID, app, cfg, fmt.Errorf("restore command failed: %w", err))
	}

	if err := rr.store.FinishRestoreRun(ctx, rec.ID, "success", nil); err != nil {
		rr.logger.Warn("backup: record restore success", "config", cfg.ID, "err", err)
	}
	if rr.notifier != nil {
		rr.notifier.RestoreFinished(app.Name, app.ID, cfg.ServiceName, true)
	}
	rr.logger.Info("backup: restore completed", "app", app.Slug, "service", cfg.ServiceName, "run", sourceRunID)
	return nil
}

// resolveCommand maps the config's stored backup command to its restore command.
func (rr *Restorer) resolveCommand(cfg store.BackupConfig) (string, bool) {
	if rr.resolver == nil {
		return "", false
	}
	return rr.resolver.RestoreCommandFor(cfg.Command)
}

// fail records the restore as failed, fires the notification, and returns the
// error (mirrors Engine.fail).
func (rr *Restorer) fail(ctx context.Context, restoreID string, app store.App, cfg store.BackupConfig, cause error) error {
	msg := cause.Error()
	if err := rr.store.FinishRestoreRun(ctx, restoreID, "failed", &msg); err != nil {
		rr.logger.Warn("backup: record restore failure", "config", cfg.ID, "err", err)
	}
	if rr.notifier != nil {
		rr.notifier.RestoreFinished(app.Name, app.ID, cfg.ServiceName, false)
	}
	rr.logger.Warn("backup: restore failed", "app", app.Slug, "service", cfg.ServiceName, "err", msg)
	return cause
}
