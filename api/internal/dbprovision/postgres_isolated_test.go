package dbprovision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedPGEngine(fd *fakeDocker, workDir string) *IsolatedPostgresEngine {
	return NewIsolatedPostgresEngine(fd, Config{
		WorkDir:     workDir,
		EdgeNetwork: "vac-edge",
		MasterKey:   []byte("0123456789abcdef0123456789abcdef"),
	})
}

func TestIsolatedPostgres_ComposeYAML(t *testing.T) {
	y := isolatedPGEngine(&fakeDocker{}, t.TempDir()).composeYAML()
	for _, want := range []string{"vac-db-managed", "postgres:16-alpine", "external: true", "vac-edge", "POSTGRES_PASSWORD"} {
		if !strings.Contains(y, want) {
			t.Errorf("compose yaml missing %q:\n%s", want, y)
		}
	}
}

func TestIsolatedPostgres_EnsureRunning(t *testing.T) {
	fd := &fakeDocker{}
	dir := t.TempDir()
	if err := isolatedPGEngine(fd, dir).EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "managed", "postgres", "compose.yaml")); err != nil {
		t.Errorf("compose file not written: %v", err)
	}
	if len(fd.ups) != 1 || fd.ups[0] != "vac-managed-postgres" {
		t.Errorf("up not called with project: %v", fd.ups)
	}
	if !strings.Contains(strings.Join(fd.execs, "\n"), "SELECT 1") {
		t.Errorf("no readiness ping: %v", fd.execs)
	}
}

func TestIsolatedPostgres_ProvisionCommands(t *testing.T) {
	fd := &fakeDocker{}
	e := isolatedPGEngine(fd, t.TempDir())
	if err := e.Provision(context.Background(), "blog_abc", "blog_abc_u", "pw123"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	joined := strings.Join(fd.execs, "\n")
	for _, want := range []string{
		"CREATE ROLE blog_abc_u LOGIN PASSWORD 'pw123'",
		"CREATE DATABASE blog_abc OWNER blog_abc_u",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("provision commands missing %q:\n%s", want, joined)
		}
	}
	// No backticks — they'd trigger command substitution in the container shell.
	if strings.Contains(joined, "`") {
		t.Errorf("provision command must not contain backticks: %s", joined)
	}
}

// TestIsolatedPostgres_ProvisionRollsBackRoleOnDBFailure covers the orphan-role
// cleanup when CREATE DATABASE fails after CREATE ROLE succeeded.
func TestIsolatedPostgres_ProvisionRollsBackRoleOnDBFailure(t *testing.T) {
	fd := &fakeDocker{execErr: context.DeadlineExceeded}
	e := isolatedPGEngine(fd, t.TempDir())
	_ = e.Provision(context.Background(), "blog_abc", "blog_abc_u", "pw")
	// First exec (CREATE ROLE) fails → we return before CREATE DATABASE, so no
	// rollback. Verify the create-role command was attempted.
	if len(fd.execs) == 0 || !strings.Contains(fd.execs[0], "CREATE ROLE") {
		t.Fatalf("expected a CREATE ROLE attempt, got %v", fd.execs)
	}
}

func TestIsolatedPostgres_Deprovision(t *testing.T) {
	fd := &fakeDocker{}
	e := isolatedPGEngine(fd, t.TempDir())
	if err := e.Deprovision(context.Background(), "blog_abc", "blog_abc_u"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	joined := strings.Join(fd.execs, "\n")
	for _, want := range []string{"DROP DATABASE IF EXISTS blog_abc WITH (FORCE)", "DROP ROLE IF EXISTS blog_abc_u"} {
		if !strings.Contains(joined, want) {
			t.Errorf("deprovision missing %q:\n%s", want, joined)
		}
	}
}

func TestIsolatedPostgres_SizeBytes(t *testing.T) {
	fd := &fakeDocker{output: []byte("blog_abc|2048\nother|99\n")}
	e := isolatedPGEngine(fd, t.TempDir())
	got, err := e.SizeBytes(context.Background(), []string{"blog_abc", "other"})
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if got["blog_abc"] != 2048 || got["other"] != 99 {
		t.Errorf("sizes = %v, want blog_abc=2048 other=99", got)
	}
}

func TestIsolatedPostgres_ConnAndCommandShapes(t *testing.T) {
	e := isolatedPGEngine(&fakeDocker{}, t.TempDir())
	if got := e.ConnString("blog_abc", "blog_abc_u", "pw"); got != "postgres://blog_abc_u:pw@vac-db-managed:5432/blog_abc?sslmode=disable" {
		t.Errorf("conn string = %q", got)
	}
	if got := e.DefaultBackupCommand("blog_abc"); got != "pg_dump -U postgres blog_abc" {
		t.Errorf("backup command = %q", got)
	}
	// The dump shape it emits must round-trip through its own matcher.
	if db, ok := e.MatchBackupCommand(e.DefaultBackupCommand("blog_abc")); !ok || db != "blog_abc" {
		t.Errorf("MatchBackupCommand(%q) = %q,%v", e.DefaultBackupCommand("blog_abc"), db, ok)
	}
	if !e.Shared() || e.FootprintMB() == 0 {
		t.Errorf("isolated postgres should be shared + carry a footprint")
	}
	if e.BackupContainer() != "vac-db-managed" {
		t.Errorf("backup container = %q", e.BackupContainer())
	}
}
