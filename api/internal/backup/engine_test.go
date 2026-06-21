package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// --- fakes ---

type fakeExec struct {
	output     []byte
	err        error
	gotID      string
	gotCommand []string
}

func (f *fakeExec) Exec(_ context.Context, containerID string, cmd []string, out io.Writer) error {
	f.gotID = containerID
	f.gotCommand = cmd
	if len(f.output) > 0 {
		_, _ = out.Write(f.output)
	}
	return f.err
}

type recordedRun struct {
	status string
	size   *int64
	key    *string
	errMsg *string
}

type fakeStore struct {
	app      store.App
	svc      store.Service
	svcErr   error
	runID    string
	recorded []recordedRun
}

func (f *fakeStore) GetApp(context.Context, string) (store.App, error) { return f.app, nil }
func (f *fakeStore) GetService(context.Context, string, string) (store.Service, error) {
	return f.svc, f.svcErr
}

func (f *fakeStore) CreateBackupRun(context.Context, string) (store.BackupRun, error) {
	return store.BackupRun{ID: f.runID}, nil
}

func (f *fakeStore) FinishBackupRun(_ context.Context, _ string, status string, size *int64, key *string, errMsg *string) error {
	f.recorded = append(f.recorded, recordedRun{status: status, size: size, key: key, errMsg: errMsg})
	return nil
}

func (f *fakeStore) PruneBackupRuns(context.Context, string, int) (int64, error) { return 0, nil }

type fakeNotifier struct{ calls int }

func (f *fakeNotifier) BackupFailed(string, string, string, string) { f.calls++ }

func cid(s string) *string { return &s }

func newFakeStore() *fakeStore {
	return &fakeStore{
		app:   store.App{ID: "app1", Slug: "blog", Name: "Blog"},
		svc:   store.Service{AppID: "app1", ServiceName: "db", ContainerID: cid("container123")},
		runID: "run1",
	}
}

func TestEngine_RunOnce_Success(t *testing.T) {
	workDir := t.TempDir()
	fs := newFakeStore()
	ex := &fakeExec{output: []byte("PGDUMPDATA")}
	nf := &fakeNotifier{}
	eng := NewEngine(fs, ex, nil, workDir, nf, nil)
	eng.now = func() time.Time { return time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC) }

	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump -U $POSTGRES_USER", Destination: "local", KeepCount: 7}
	if err := eng.RunOnce(context.Background(), cfg); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if ex.gotID != "container123" {
		t.Errorf("exec container = %q, want container123", ex.gotID)
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "success" {
		t.Fatalf("recorded = %+v, want one success", fs.recorded)
	}
	if fs.recorded[0].size == nil || *fs.recorded[0].size != 10 {
		t.Errorf("size = %v, want 10", fs.recorded[0].size)
	}
	if nf.calls != 0 {
		t.Errorf("notifier fired %d times on success", nf.calls)
	}
	// Artifact landed at the expected key.
	want := filepath.Join(workDir, "backups", "blog", "db", "20260601T030000Z-run1.dump")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if string(data) != "PGDUMPDATA" {
		t.Errorf("artifact body = %q", data)
	}
}

func TestEngine_RunOnce_ExecFailureNotifies(t *testing.T) {
	fs := newFakeStore()
	ex := &fakeExec{err: errors.New("exit status 1")}
	nf := &fakeNotifier{}
	eng := NewEngine(fs, ex, nil, t.TempDir(), nf, nil)

	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump", Destination: "local", KeepCount: 7}
	if err := eng.RunOnce(context.Background(), cfg); err == nil {
		t.Fatal("RunOnce: expected error")
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "failed" {
		t.Fatalf("recorded = %+v, want one failed", fs.recorded)
	}
	if fs.recorded[0].errMsg == nil {
		t.Error("failed run recorded without an error message")
	}
	if nf.calls != 1 {
		t.Errorf("notifier fired %d times, want 1", nf.calls)
	}
}

// TestEngine_RunOnce_ExplicitContainer covers a managed-DB backup (00080): when
// ContainerName is set the engine execs into it directly and never consults the
// service row — so a service lookup error must not matter.
func TestEngine_RunOnce_ExplicitContainer(t *testing.T) {
	fs := newFakeStore()
	fs.svcErr = errors.New("no such service") // would fail if resolveContainer touched it
	ex := &fakeExec{output: []byte("DUMP")}
	eng := NewEngine(fs, ex, nil, t.TempDir(), &fakeNotifier{}, nil)
	eng.now = func() time.Time { return time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC) }

	container := "vac-db"
	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "blog_abc", ContainerName: &container, Command: "pg_dump -U vac blog_abc", Destination: "local", KeepCount: 7}
	if err := eng.RunOnce(context.Background(), cfg); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if ex.gotID != "vac-db" {
		t.Errorf("exec container = %q, want vac-db (the explicit container)", ex.gotID)
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "success" {
		t.Fatalf("recorded = %+v, want one success", fs.recorded)
	}
}

func TestEngine_RunOnce_NoContainer(t *testing.T) {
	fs := newFakeStore()
	fs.svc.ContainerID = nil
	nf := &fakeNotifier{}
	eng := NewEngine(fs, &fakeExec{}, nil, t.TempDir(), nf, nil)

	cfg := store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump", Destination: "local", KeepCount: 7}
	if err := eng.RunOnce(context.Background(), cfg); err == nil {
		t.Fatal("expected error for service with no container")
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "failed" {
		t.Fatalf("recorded = %+v, want one failed", fs.recorded)
	}
	if nf.calls != 1 {
		t.Errorf("notifier fired %d times, want 1", nf.calls)
	}
}
