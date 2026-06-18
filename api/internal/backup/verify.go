package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrVerifyUnsupported is returned when a config's backup command isn't a
// recognized engine default, so VAC can't build a restorability check for it
// (same constraint as restore — VAC only verifies what it knows how to invert).
var ErrVerifyUnsupported = errors.New("backup: verification unsupported for this command")

// ErrVerifyInProgress is returned when a verification for the same config is
// already running (one at a time per config).
var ErrVerifyInProgress = errors.New("backup: a verification is already running for this config")

// ErrNoArtifact is returned when a config has no recorded successful run to
// verify yet.
var ErrNoArtifact = errors.New("backup: no successful backup to verify")

// VerifyCommandResolver maps a stored backup command to a non-destructive
// restorability check that replays the dump into a throwaway scratch database.
// *dbprovision.Provisioner satisfies it.
type VerifyCommandResolver interface {
	VerifyCommandFor(backupCommand, scratchDB string) (verifyCommand string, ok bool)
}

// VerifyNotifier fires the backup-unverified event. *notify.Dispatcher satisfies
// it.
type VerifyNotifier interface {
	BackupUnverified(appName, appID, service, errMsg string)
}

// VerifierStore is the persistence slice the Verifier depends on.
type VerifierStore interface {
	serviceGetter
	GetApp(ctx context.Context, id string) (store.App, error)
	LatestBackupRun(ctx context.Context, configID string) (store.BackupRun, error)
	CreateVerification(ctx context.Context, configID, sourceRunID string) (store.BackupVerification, error)
	FinishVerification(ctx context.Context, verificationID, status string, errMsg *string) error
	LatestVerification(ctx context.Context, configID string) (store.BackupVerification, error)
}

// Verifier confirms a backup is actually restorable without touching live data:
// it reads the latest successful run's artifact back, replays it into a
// throwaway scratch database on the same engine, and drops the scratch DB. A
// clean replay → verified; any replay error → the backup may not be restorable.
type Verifier struct {
	store    VerifierStore
	exec     ExecStdinRunner
	box      *crypto.Box
	workDir  string
	resolver VerifyCommandResolver
	notifier VerifyNotifier
	logger   *slog.Logger
}

// NewVerifier wires the verifier. notifier may be nil (failures still recorded).
func NewVerifier(s VerifierStore, exec ExecStdinRunner, box *crypto.Box, workDir string, resolver VerifyCommandResolver, notifier VerifyNotifier, logger *slog.Logger) *Verifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Verifier{store: s, exec: exec, box: box, workDir: workDir, resolver: resolver, notifier: notifier, logger: logger}
}

// CanVerify reports whether cfg's backup command maps to a known restorability
// check (only managed-engine defaults; a custom command can't be inverted).
func (v *Verifier) CanVerify(cfg store.BackupConfig) bool {
	if v.resolver == nil {
		return false
	}
	_, ok := v.resolver.VerifyCommandFor(cfg.Command, "vac_verify_probe")
	return ok
}

// VerifyOnce runs a restorability check for cfg's latest successful backup. It
// records a backup_verifications row (running → success/failed) and fires the
// unverified event on failure. The returned error mirrors the recorded outcome
// (nil on a clean replay).
func (v *Verifier) VerifyOnce(ctx context.Context, cfg store.BackupConfig) error {
	app, err := v.store.GetApp(ctx, cfg.AppID)
	if err != nil {
		return fmt.Errorf("backup: load app: %w", err)
	}

	scratch, err := scratchDBName()
	if err != nil {
		return err
	}
	verifyCmd, ok := v.resolver.VerifyCommandFor(cfg.Command, scratch)
	if !ok {
		return ErrVerifyUnsupported
	}

	run, err := v.store.LatestBackupRun(ctx, cfg.ID)
	if err != nil {
		return ErrNoArtifact
	}
	if run.Status != "success" || run.ArtifactKey == nil {
		return ErrNoArtifact
	}

	// One verification at a time per config — the row is the guard.
	if latest, err := v.store.LatestVerification(ctx, cfg.ID); err == nil && latest.Status == "running" {
		return ErrVerifyInProgress
	}

	rec, err := v.store.CreateVerification(ctx, cfg.ID, run.ID)
	if err != nil {
		return fmt.Errorf("backup: open verification: %w", err)
	}

	container, err := resolveContainer(ctx, v.store, cfg)
	if err != nil {
		return v.fail(ctx, rec.ID, app, cfg, err)
	}
	dest, err := NewDestination(cfg, v.box, v.workDir)
	if err != nil {
		return v.fail(ctx, rec.ID, app, cfg, err)
	}
	reader, err := dest.Open(ctx, *run.ArtifactKey)
	if err != nil {
		return v.fail(ctx, rec.ID, app, cfg, fmt.Errorf("open artifact: %w", err))
	}
	defer func() { _ = reader.Close() }()

	if err := v.exec.ExecStdin(ctx, container, []string{verifyCmd}, reader); err != nil {
		return v.fail(ctx, rec.ID, app, cfg, fmt.Errorf("test restore failed: %w", err))
	}

	if err := v.store.FinishVerification(ctx, rec.ID, "success", nil); err != nil {
		v.logger.Warn("backup: record verification success", "config", cfg.ID, "err", err)
	}
	v.logger.Info("backup: verification passed", "app", app.Slug, "service", cfg.ServiceName, "run", run.ID)
	return nil
}

// fail records the verification as failed, fires the notification, and returns
// the error (mirrors Restorer.fail).
func (v *Verifier) fail(ctx context.Context, verificationID string, app store.App, cfg store.BackupConfig, cause error) error {
	msg := cause.Error()
	if err := v.store.FinishVerification(ctx, verificationID, "failed", &msg); err != nil {
		v.logger.Warn("backup: record verification failure", "config", cfg.ID, "err", err)
	}
	if v.notifier != nil {
		v.notifier.BackupUnverified(app.Name, app.ID, cfg.ServiceName, msg)
	}
	v.logger.Warn("backup: verification failed", "app", app.Slug, "service", cfg.ServiceName, "err", msg)
	return cause
}

// scratchDBName returns a unique, identifier-safe throwaway database name
// (vac_verify_<random>). Lowercase + digits only so it's safe to interpolate
// into the engine's DDL, matching the generated-name shape the engines expect.
func scratchDBName() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	limit := 256 - (256 % len(alphabet))
	out := make([]byte, 10)
	var b [1]byte
	for i := 0; i < len(out); {
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("backup: rand: %w", err)
		}
		if int(b[0]) >= limit {
			continue
		}
		out[i] = alphabet[int(b[0])%len(alphabet)]
		i++
	}
	return "vac_verify_" + string(out), nil
}
