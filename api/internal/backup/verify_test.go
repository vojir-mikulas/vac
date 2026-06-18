package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type recordedVerification struct {
	status string
	errMsg *string
}

type fakeVerifyStore struct {
	app       store.App
	svc       store.Service
	svcErr    error
	run       store.BackupRun
	runErr    error
	latest    store.BackupVerification
	latestErr error
	recorded  []recordedVerification
}

func (f *fakeVerifyStore) GetApp(context.Context, string) (store.App, error) { return f.app, nil }
func (f *fakeVerifyStore) GetService(context.Context, string, string) (store.Service, error) {
	return f.svc, f.svcErr
}
func (f *fakeVerifyStore) LatestBackupRun(context.Context, string) (store.BackupRun, error) {
	return f.run, f.runErr
}
func (f *fakeVerifyStore) CreateVerification(_ context.Context, configID, sourceRunID string) (store.BackupVerification, error) {
	return store.BackupVerification{ID: "verify1", ConfigID: configID, SourceRunID: sourceRunID, Status: "running"}, nil
}
func (f *fakeVerifyStore) FinishVerification(_ context.Context, _, status string, errMsg *string) error {
	f.recorded = append(f.recorded, recordedVerification{status: status, errMsg: errMsg})
	return nil
}
func (f *fakeVerifyStore) LatestVerification(context.Context, string) (store.BackupVerification, error) {
	return f.latest, f.latestErr
}

// fakeVerifyResolver records the scratch DB name it was handed.
type fakeVerifyResolver struct {
	ok         bool
	gotScratch string
}

func (f *fakeVerifyResolver) VerifyCommandFor(_ string, scratchDB string) (string, bool) {
	f.gotScratch = scratchDB
	return "createdb " + scratchDB + " && replay", f.ok
}

type fakeVerifyNotifier struct{ calls int }

func (f *fakeVerifyNotifier) BackupUnverified(_, _, _, _ string) { f.calls++ }

func newVerifyStore() *fakeVerifyStore {
	return &fakeVerifyStore{
		app:       store.App{ID: "app1", Slug: "blog", Name: "Blog"},
		svc:       store.Service{AppID: "app1", ServiceName: "db", ContainerID: cid("container123")},
		run:       store.BackupRun{ID: "run1", ConfigID: "cfg1", Status: "success", ArtifactKey: cid("blog/db/x.dump")},
		latestErr: store.ErrNotFound,
	}
}

func verifyCfg() store.BackupConfig {
	return store.BackupConfig{ID: "cfg1", AppID: "app1", ServiceName: "db", Command: "pg_dump -U vac blog_x", Destination: "local"}
}

func TestVerifier_Success(t *testing.T) {
	workDir := t.TempDir()
	seedArtifact(t, workDir, "blog/db/x.dump", "SQLDUMP")
	fs := newVerifyStore()
	ex := &fakeStdinExec{}
	res := &fakeVerifyResolver{ok: true}
	nf := &fakeVerifyNotifier{}
	v := NewVerifier(fs, ex, nil, workDir, res, nf, nil)

	if err := v.VerifyOnce(context.Background(), verifyCfg()); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}
	if ex.gotID != "container123" || ex.gotIn != "SQLDUMP" {
		t.Errorf("exec container=%q stdin=%q", ex.gotID, ex.gotIn)
	}
	if !strings.HasPrefix(res.gotScratch, "vac_verify_") {
		t.Errorf("scratch DB = %q, want vac_verify_ prefix", res.gotScratch)
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "success" {
		t.Fatalf("recorded = %+v, want one success", fs.recorded)
	}
	if nf.calls != 0 {
		t.Errorf("notifier called %d times on success, want 0", nf.calls)
	}
}

func TestVerifier_ExecFailureRecordsAndNotifies(t *testing.T) {
	workDir := t.TempDir()
	seedArtifact(t, workDir, "blog/db/x.dump", "SQLDUMP")
	fs := newVerifyStore()
	ex := &fakeStdinExec{err: errors.New("relation does not exist")}
	nf := &fakeVerifyNotifier{}
	v := NewVerifier(fs, ex, nil, workDir, &fakeVerifyResolver{ok: true}, nf, nil)

	if err := v.VerifyOnce(context.Background(), verifyCfg()); err == nil {
		t.Fatal("expected error on exec failure")
	}
	if len(fs.recorded) != 1 || fs.recorded[0].status != "failed" {
		t.Fatalf("recorded = %+v, want one failed", fs.recorded)
	}
	if nf.calls != 1 {
		t.Errorf("notifier calls = %d, want 1", nf.calls)
	}
}

func TestVerifier_RefusesUnknownCommand(t *testing.T) {
	v := NewVerifier(newVerifyStore(), &fakeStdinExec{}, nil, t.TempDir(), &fakeVerifyResolver{ok: false}, nil, nil)
	if err := v.VerifyOnce(context.Background(), verifyCfg()); !errors.Is(err, ErrVerifyUnsupported) {
		t.Errorf("err = %v, want ErrVerifyUnsupported", err)
	}
}

func TestVerifier_NoArtifact(t *testing.T) {
	fs := newVerifyStore()
	fs.runErr = store.ErrNotFound // no successful run to verify
	v := NewVerifier(fs, &fakeStdinExec{}, nil, t.TempDir(), &fakeVerifyResolver{ok: true}, nil, nil)
	if err := v.VerifyOnce(context.Background(), verifyCfg()); !errors.Is(err, ErrNoArtifact) {
		t.Errorf("err = %v, want ErrNoArtifact", err)
	}
}

func TestVerifier_RefusesConcurrent(t *testing.T) {
	workDir := t.TempDir()
	seedArtifact(t, workDir, "blog/db/x.dump", "SQLDUMP")
	fs := newVerifyStore()
	fs.latest = store.BackupVerification{ID: "running1", Status: "running"}
	fs.latestErr = nil
	v := NewVerifier(fs, &fakeStdinExec{}, nil, workDir, &fakeVerifyResolver{ok: true}, nil, nil)
	if err := v.VerifyOnce(context.Background(), verifyCfg()); !errors.Is(err, ErrVerifyInProgress) {
		t.Errorf("err = %v, want ErrVerifyInProgress", err)
	}
}
