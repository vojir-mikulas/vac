package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// --- fakes ---

type fakeStdinExec struct {
	gotID  string
	gotCmd []string
	gotIn  string
	err    error
}

func (f *fakeStdinExec) ExecStdin(_ context.Context, containerID string, cmd []string, stdin io.Reader) error {
	f.gotID = containerID
	f.gotCmd = cmd
	b, _ := io.ReadAll(stdin)
	f.gotIn = string(b)
	return f.err
}

type recordedRestore struct {
	status string
	errMsg *string
}

type fakeRestoreStore struct {
	app       store.App
	svc       store.Service
	svcErr    error
	run       store.BackupRun
	runErr    error
	latest    store.BackupRestore
	latestErr error
	recorded  []recordedRestore
}

func (f *fakeRestoreStore) GetApp(context.Context, string) (store.App, error) { return f.app, nil }
func (f *fakeRestoreStore) GetService(context.Context, string, string) (store.Service, error) {
	return f.svc, f.svcErr
}

func (f *fakeRestoreStore) GetBackupRun(context.Context, string) (store.BackupRun, error) {
	return f.run, f.runErr
}

func (f *fakeRestoreStore) CreateRestoreRun(_ context.Context, configID, sourceRunID string) (store.BackupRestore, error) {
	return store.BackupRestore{ID: "restore1", ConfigID: configID, SourceRunID: sourceRunID, Status: "running"}, nil
}

func (f *fakeRestoreStore) FinishRestoreRun(_ context.Context, _, status string, errMsg *string) error {
	f.recorded = append(f.recorded, recordedRestore{status: status, errMsg: errMsg})
	return nil
}

func (f *fakeRestoreStore) LatestRestoreRun(context.Context, string) (store.BackupRestore, error) {
	return f.latest, f.latestErr
}

// fakeResolver maps any command to a fixed restore command unless ok is false.
type fakeResolver struct {
	cmd string
	ok  bool
}

func (f fakeResolver) RestoreCommandFor(string) (string, bool) { return f.cmd, f.ok }

type fakeRestoreNotifier struct {
	calls int
	lasOK bool
}

func (f *fakeRestoreNotifier) RestoreFinished(_, _, _ string, ok bool) {
	f.calls++
	f.lasOK = ok
}

func newRestoreStore() *fakeRestoreStore {
	return &fakeRestoreStore{
		app:       store.App{ID: "app1", Slug: "blog", Name: "Blog"},
		svc:       store.Service{AppID: "app1", ServiceName: "db", ContainerID: cid("container123")},
		run:       store.BackupRun{ID: "run1", ConfigID: "cfg1", Status: "success", ArtifactKey: cid("blog/db/x.dump")},
		latestErr: store.ErrNotFound,
	}
}

// seedArtifact writes a local artifact the Destination can Open.
func seedArtifact(t *testing.T, workDir, key, body string) {
	t.Helper()
	p := filepath.Join(workDir, "backups", filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestRestorer_Restore_Success(t *testing.T) {
	workDir := t.TempDir()
	seedArtifact(t, workDir, "blog/db/x.dump", "SQLDUMP")
	fs := newRestoreStore()
	ex := &fakeStdinExec{}
	nf := &fakeRestoreNotifier{}
	rr := NewRestorer(fs, ex, nil, workDir, fakeResolver{cmd: "psql -U vac -d blog_x", ok: true}, nf, nil)

	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump -U vac blog_x", Destination: "local"}
	if err := rr.Restore(context.Background(), cfg, "run1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ex.gotID != "container123" {
		t.Errorf("exec container = %q, want container123", ex.gotID)
	}
	if ex.gotIn != "SQLDUMP" {
		t.Errorf("piped stdin = %q, want SQLDUMP", ex.gotIn)
	}
	if len(ex.gotCmd) != 1 || ex.gotCmd[0] != "psql -U vac -d blog_x" {
		t.Errorf("restore cmd = %v", ex.gotCmd)
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "success" {
		t.Fatalf("recorded = %+v, want one success", fs.recorded)
	}
	if nf.calls != 1 || !nf.lasOK {
		t.Errorf("notifier calls=%d ok=%v, want 1 true", nf.calls, nf.lasOK)
	}
}

func TestRestorer_Restore_RefusesUnknownCommand(t *testing.T) {
	fs := newRestoreStore()
	rr := NewRestorer(fs, &fakeStdinExec{}, nil, t.TempDir(), fakeResolver{ok: false}, nil, nil)
	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "custom-dump.sh", Destination: "local"}
	if err := rr.Restore(context.Background(), cfg, "run1"); !errors.Is(err, ErrRestoreUnsupported) {
		t.Fatalf("err = %v, want ErrRestoreUnsupported", err)
	}
	// No run row should have been created/finished for a refused restore.
	if len(fs.recorded) != 0 {
		t.Errorf("recorded = %+v, want none", fs.recorded)
	}
}

func TestRestorer_Restore_RefusesConcurrent(t *testing.T) {
	fs := newRestoreStore()
	fs.latest = store.BackupRestore{ID: "r0", Status: "running"}
	fs.latestErr = nil
	rr := NewRestorer(fs, &fakeStdinExec{}, nil, t.TempDir(), fakeResolver{cmd: "psql", ok: true}, nil, nil)
	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump -U vac blog_x", Destination: "local"}
	if err := rr.Restore(context.Background(), cfg, "run1"); !errors.Is(err, ErrRestoreInProgress) {
		t.Fatalf("err = %v, want ErrRestoreInProgress", err)
	}
}

func TestRestorer_Restore_ExecFailureNotifies(t *testing.T) {
	workDir := t.TempDir()
	seedArtifact(t, workDir, "blog/db/x.dump", "SQLDUMP")
	fs := newRestoreStore()
	ex := &fakeStdinExec{err: errors.New("exit status 1")}
	nf := &fakeRestoreNotifier{}
	rr := NewRestorer(fs, ex, nil, workDir, fakeResolver{cmd: "psql", ok: true}, nf, nil)
	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump -U vac blog_x", Destination: "local"}
	if err := rr.Restore(context.Background(), cfg, "run1"); err == nil {
		t.Fatal("expected error")
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "failed" || fs.recorded[0].errMsg == nil {
		t.Fatalf("recorded = %+v, want one failed with message", fs.recorded)
	}
	if nf.calls != 1 || nf.lasOK {
		t.Errorf("notifier calls=%d ok=%v, want 1 false", nf.calls, nf.lasOK)
	}
}
