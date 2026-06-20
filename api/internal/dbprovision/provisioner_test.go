package dbprovision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
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

func (e *fakeEngine) SizeBytes(_ context.Context, dbNames []string) (map[string]int64, error) {
	out := make(map[string]int64, len(dbNames))
	for i, n := range dbNames {
		out[n] = int64((i + 1) * 1000)
	}
	return out, nil
}

func (e *fakeEngine) ConnString(db, role, pw string) string {
	return "proto://" + role + ":" + pw + "@host/" + db
}
func (e *fakeEngine) DefaultBackupCommand(db string) string { return "dump " + db }
func (e *fakeEngine) MatchBackupCommand(cmd string) (string, bool) {
	return strings.CutPrefix(cmd, "dump ")
}
func (e *fakeEngine) RestoreCommand(db string) string            { return "restore " + db }
func (e *fakeEngine) VerifyRestoreCommand(scratch string) string { return "verify " + scratch }
func (e *fakeEngine) BackupContainer() string                    { return "vac-" + e.name }
func (e *fakeEngine) EnvVarName() string                         { return "DATABASE_URL" }
func (e *fakeEngine) FootprintMB() int                           { return 0 }
func (e *fakeEngine) Shared() bool                               { return false }

type fakeProvStore struct {
	app           store.App
	dbs           map[string]store.ManagedDatabase
	envVars       map[string][]byte
	backupConfigs int
	// backupCfgByService records seeded configs keyed by service_name, mirroring the
	// real UNIQUE(app_id, service_name) so the two-DBs-one-engine fix is testable.
	backupCfgByService map[string]store.BackupConfigInput
	statuses           map[string]string
}

func newFakeProvStore() *fakeProvStore {
	return &fakeProvStore{
		app:                store.App{ID: "app1", Slug: "blog"},
		dbs:                map[string]store.ManagedDatabase{},
		envVars:            map[string][]byte{},
		backupCfgByService: map[string]store.BackupConfigInput{},
		statuses:           map[string]string{},
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

func (s *fakeProvStore) ListAllManagedDatabases(_ context.Context) ([]store.ManagedDatabaseWithApp, error) {
	var out []store.ManagedDatabaseWithApp
	for _, m := range s.dbs {
		out = append(out, store.ManagedDatabaseWithApp{ManagedDatabase: m, AppSlug: s.app.Slug, AppName: s.app.Name})
	}
	return out, nil
}

func (s *fakeProvStore) GetBackupConfigForService(_ context.Context, _, service string) (store.BackupConfig, error) {
	in, ok := s.backupCfgByService[service]
	if !ok {
		return store.BackupConfig{}, store.ErrNotFound
	}
	return store.BackupConfig{ID: "cfg-" + service, ServiceName: in.ServiceName, ContainerName: in.ContainerName, Command: in.Command}, nil
}

func (s *fakeProvStore) LatestBackupRun(_ context.Context, _ string) (store.BackupRun, error) {
	return store.BackupRun{}, store.ErrNotFound
}

func (s *fakeProvStore) UpsertEnvVar(_ context.Context, _, key string, value []byte, _ bool) error {
	s.envVars[key] = value
	return nil
}

func (s *fakeProvStore) DeleteEnvVar(_ context.Context, _, key string) error {
	delete(s.envVars, key)
	return nil
}

func (s *fakeProvStore) CreateBackupConfig(_ context.Context, _ string, in store.BackupConfigInput) (store.BackupConfig, error) {
	if _, exists := s.backupCfgByService[in.ServiceName]; exists {
		return store.BackupConfig{}, store.ErrConflict
	}
	s.backupConfigs++
	s.backupCfgByService[in.ServiceName] = in
	return store.BackupConfig{ID: "cfg-" + in.ServiceName, ServiceName: in.ServiceName, ContainerName: in.ContainerName}, nil
}

func (s *fakeProvStore) DeleteBackupConfigForService(_ context.Context, _, service string) error {
	delete(s.backupCfgByService, service)
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

// TestProvisioner_TwoDBsSameEngineEachGetBackup covers the 00080 fix: two managed
// DBs of the same engine on one app share an engine container but must each get
// their own backup config (keyed on the DB name, with the container as the exec
// target) — previously the second silently collided and was never backed up.
func TestProvisioner_TwoDBsSameEngineEachGetBackup(t *testing.T) {
	st := newFakeProvStore()
	eng := &fakeEngine{name: "postgres"}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()

	for _, db := range []string{"blog_abc", "blog_def"} {
		names := GeneratedNames{DBName: db, RoleName: db + "_u", Password: "pw"}
		row, _ := st.CreateManagedDatabase(context.Background(), "app1", "postgres", db, &names.RoleName, []byte("sealed"), "DATABASE_URL_"+db)
		p.provision(row, st.app, eng, names, []byte("sealed"))
	}

	if st.backupConfigs != 2 {
		t.Fatalf("backup configs seeded = %d, want 2 (one per DB)", st.backupConfigs)
	}
	for _, db := range []string{"blog_abc", "blog_def"} {
		in, ok := st.backupCfgByService[db]
		if !ok {
			t.Fatalf("no backup config keyed on DB name %q", db)
		}
		if in.ContainerName == nil || *in.ContainerName != "vac-postgres" {
			t.Errorf("config %q container_name = %v, want vac-postgres", db, in.ContainerName)
		}
		if in.Command != "dump "+db {
			t.Errorf("config %q command = %q, want %q", db, in.Command, "dump "+db)
		}
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
	if _, err := p.Add(context.Background(), st.app, "oracle", ""); !errors.Is(err, ErrUnsupportedEngine) {
		t.Errorf("err = %v, want ErrUnsupportedEngine", err)
	}
}

// TestProvisioner_ResolveBindingName covers the DATABASE_URL collision fix
// (P1.1): a second managed DB must not silently reuse the first's binding.
func TestProvisioner_ResolveBindingName(t *testing.T) {
	eng := &fakeEngine{name: "postgres"}
	withDBs := func(names ...string) *fakeProvStore {
		st := newFakeProvStore()
		for i, n := range names {
			id := "db" + string(rune('a'+i))
			st.dbs[id] = store.ManagedDatabase{ID: id, AppID: st.app.ID, Engine: "postgres", EnvVarName: n}
		}
		return st
	}
	mk := func(st *fakeProvStore) *Provisioner {
		p := newTestProvisioner(t, st, eng)
		p.logger = discardLogger()
		return p
	}

	cases := []struct {
		name      string
		existing  []string
		requested string
		want      string
		wantErr   error
	}{
		{name: "first default → DATABASE_URL", want: "DATABASE_URL"},
		{name: "second default → suffixed by engine", existing: []string{"DATABASE_URL"}, want: "DATABASE_URL_POSTGRES"},
		{name: "third default → numbered", existing: []string{"DATABASE_URL", "DATABASE_URL_POSTGRES"}, want: "DATABASE_URL_2"},
		{name: "explicit honored", requested: "ANALYTICS_DATABASE_URL", want: "ANALYTICS_DATABASE_URL"},
		{name: "explicit duplicate conflicts", existing: []string{"DATABASE_URL"}, requested: "DATABASE_URL", wantErr: store.ErrConflict},
		{name: "malformed rejected", requested: "lower-case", wantErr: ErrInvalidBindingName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mk(withDBs(c.existing...))
			got, err := p.resolveBindingName(context.Background(), "app1", eng, c.requested)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBindingName: %v", err)
			}
			if got != c.want {
				t.Errorf("binding = %q, want %q", got, c.want)
			}
		})
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
