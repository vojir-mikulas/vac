package dbprovision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- fakes ---

type fakeEngine struct {
	name         string
	provErr      error
	ensureErr    error
	deprovCalled bool
}

func (e *fakeEngine) Name() string                        { return e.name }
func (e *fakeEngine) EnsureRunning(context.Context) error { return e.ensureErr }
func (e *fakeEngine) Provision(context.Context, string, string, string) error {
	return e.provErr
}
func (e *fakeEngine) Deprovision(context.Context, string, string) error {
	e.deprovCalled = true
	return nil
}
func (e *fakeEngine) ConnString(db, role, pw string) string {
	return "proto://" + role + ":" + pw + "@host/" + db
}
func (e *fakeEngine) DefaultBackupCommand(db string) string { return "dump " + db }
func (e *fakeEngine) BackupContainer() string               { return "vac-" + e.name }
func (e *fakeEngine) EnvVarName() string                    { return "DATABASE_URL" }
func (e *fakeEngine) FootprintMB() int                      { return 0 }
func (e *fakeEngine) Shared() bool                          { return false }

type fakeProvStore struct {
	app           store.App
	dbs           map[string]store.ManagedDatabase
	envVars       map[string][]byte
	backupConfigs int
	statuses      map[string]string
}

func newFakeProvStore() *fakeProvStore {
	return &fakeProvStore{
		app:      store.App{ID: "app1", Slug: "blog"},
		dbs:      map[string]store.ManagedDatabase{},
		envVars:  map[string][]byte{},
		statuses: map[string]string{},
	}
}

func (s *fakeProvStore) GetApp(context.Context, string) (store.App, error) { return s.app, nil }
func (s *fakeProvStore) CreateManagedDatabase(_ context.Context, appID, engine, dbName string, roleName *string, secretEnc []byte, envVarName string) (store.ManagedDatabase, error) {
	m := store.ManagedDatabase{ID: "db-" + dbName, AppID: appID, Engine: engine, DBName: dbName, RoleName: roleName, SecretEnc: secretEnc, EnvVarName: envVarName, Status: "provisioning"}
	s.dbs[m.ID] = m
	s.statuses[m.ID] = "provisioning"
	return m, nil
}
func (s *fakeProvStore) SetManagedDatabaseStatus(_ context.Context, id, status string, _ *string) error {
	s.statuses[id] = status
	return nil
}
func (s *fakeProvStore) GetManagedDatabase(_ context.Context, id string) (store.ManagedDatabase, error) {
	m, ok := s.dbs[id]
	if !ok {
		return store.ManagedDatabase{}, store.ErrNotFound
	}
	return m, nil
}
func (s *fakeProvStore) ListManagedDatabasesForApp(_ context.Context, appID string) ([]store.ManagedDatabase, error) {
	var out []store.ManagedDatabase
	for _, m := range s.dbs {
		if m.AppID == appID {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *fakeProvStore) DeleteManagedDatabase(_ context.Context, _, id string) error {
	delete(s.dbs, id)
	return nil
}
func (s *fakeProvStore) UpsertEnvVar(_ context.Context, _, key string, value []byte, _ bool) error {
	s.envVars[key] = value
	return nil
}
func (s *fakeProvStore) DeleteEnvVar(_ context.Context, _, key string) error {
	delete(s.envVars, key)
	return nil
}
func (s *fakeProvStore) CreateBackupConfig(_ context.Context, _ string, _ store.BackupConfigInput) (store.BackupConfig, error) {
	s.backupConfigs++
	return store.BackupConfig{}, nil
}
func (s *fakeProvStore) DeleteBackupConfigForService(context.Context, string, string) error {
	return nil
}

func testBox(t *testing.T) *crypto.Box {
	t.Helper()
	b, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestProvisioner(t *testing.T, st Store, eng Engine) *Provisioner {
	return &Provisioner{
		store:   st,
		box:     testBox(t),
		engines: map[string]Engine{eng.Name(): eng},
		order:   []string{eng.Name()},
		logger:  nil,
	}
}

func TestProvisioner_ProvisionSuccess(t *testing.T) {
	st := newFakeProvStore()
	eng := &fakeEngine{name: "postgres"}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()

	names := GeneratedNames{DBName: "blog_abc", RoleName: "blog_abc_u", Password: "pw"}
	row, _ := st.CreateManagedDatabase(context.Background(), "app1", "postgres", names.DBName, &names.RoleName, []byte("sealed"), "DATABASE_URL")

	p.provision(row, st.app, eng, names, []byte("sealed"))

	if st.statuses[row.ID] != "ready" {
		t.Errorf("status = %q, want ready", st.statuses[row.ID])
	}
	if _, ok := st.envVars["DATABASE_URL"]; !ok {
		t.Errorf("connection string env var not injected")
	}
	if st.backupConfigs != 1 {
		t.Errorf("backup configs seeded = %d, want 1", st.backupConfigs)
	}
}

func TestProvisioner_ProvisionFailureSetsError(t *testing.T) {
	st := newFakeProvStore()
	eng := &fakeEngine{name: "postgres", provErr: errors.New("boom")}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()

	names := GeneratedNames{DBName: "blog_abc", RoleName: "blog_abc_u", Password: "pw"}
	row, _ := st.CreateManagedDatabase(context.Background(), "app1", "postgres", names.DBName, &names.RoleName, []byte("sealed"), "DATABASE_URL")

	p.provision(row, st.app, eng, names, []byte("sealed"))

	if st.statuses[row.ID] != "error" {
		t.Errorf("status = %q, want error", st.statuses[row.ID])
	}
	if _, ok := st.envVars["DATABASE_URL"]; ok {
		t.Errorf("env var injected despite provisioning failure")
	}
}

func TestProvisioner_AddRejectsUnknownEngine(t *testing.T) {
	st := newFakeProvStore()
	p := newTestProvisioner(t, st, &fakeEngine{name: "postgres"})
	p.logger = discardLogger()
	if _, err := p.Add(context.Background(), st.app, "oracle"); !errors.Is(err, ErrUnsupportedEngine) {
		t.Errorf("err = %v, want ErrUnsupportedEngine", err)
	}
}

func TestProvisioner_Remove(t *testing.T) {
	st := newFakeProvStore()
	eng := &fakeEngine{name: "postgres"}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()

	role := "blog_abc_u"
	m := store.ManagedDatabase{ID: "db-x", AppID: "app1", Engine: "postgres", DBName: "blog_abc", RoleName: &role, EnvVarName: "DATABASE_URL"}
	st.dbs[m.ID] = m
	st.envVars["DATABASE_URL"] = []byte("x")

	if err := p.Remove(context.Background(), "app1", "db-x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !eng.deprovCalled {
		t.Error("engine Deprovision not called")
	}
	if _, ok := st.dbs["db-x"]; ok {
		t.Error("row not deleted")
	}
	if _, ok := st.envVars["DATABASE_URL"]; ok {
		t.Error("env var not removed")
	}
}

func TestProvisioner_DeprovisionApp(t *testing.T) {
	st := newFakeProvStore()
	eng := &fakeEngine{name: "postgres"}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()
	role := "r"
	st.dbs["db-x"] = store.ManagedDatabase{ID: "db-x", AppID: "app1", Engine: "postgres", DBName: "d", RoleName: &role}
	p.DeprovisionApp(context.Background(), "app1")
	if !eng.deprovCalled {
		t.Error("engine Deprovision not called for app delete")
	}
}
