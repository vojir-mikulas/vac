package dbprovision

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

type fakeDocker struct {
	ups     []string
	execs   []string
	execErr error
}

func (f *fakeDocker) Up(_ context.Context, _, _, projectName, _ string, _ ...string) error {
	f.ups = append(f.ups, projectName)
	return nil
}
func (f *fakeDocker) Ps(_ context.Context, _ string) ([]dockercli.PsService, error) { return nil, nil }
func (f *fakeDocker) Exec(_ context.Context, _ string, cmd []string, _ io.Writer) error {
	f.execs = append(f.execs, cmd[0])
	return f.execErr
}

func mariadbEngine(fd *fakeDocker, workDir string) *MariaDBEngine {
	return NewMariaDBEngine(fd, Config{
		WorkDir:     workDir,
		EdgeNetwork: "vac-edge",
		MasterKey:   []byte("0123456789abcdef0123456789abcdef"),
	})
}

func TestMariaDBEngine_ComposeYAML(t *testing.T) {
	y := mariadbEngine(&fakeDocker{}, t.TempDir()).composeYAML()
	for _, want := range []string{"vac-mariadb", "mariadb:11", "external: true", "vac-edge", "MARIADB_ROOT_PASSWORD"} {
		if !strings.Contains(y, want) {
			t.Errorf("compose yaml missing %q:\n%s", want, y)
		}
	}
}

func TestMariaDBEngine_EnsureRunning(t *testing.T) {
	fd := &fakeDocker{}
	dir := t.TempDir()
	e := mariadbEngine(fd, dir)
	if err := e.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "managed", "mariadb", "compose.yaml")); err != nil {
		t.Errorf("compose file not written: %v", err)
	}
	if len(fd.ups) != 1 || fd.ups[0] != "vac-managed-mariadb" {
		t.Errorf("up not called with project: %v", fd.ups)
	}
	// A readiness ping then the .my.cnf write should have run.
	joined := strings.Join(fd.execs, "\n")
	if !strings.Contains(joined, "SELECT 1") {
		t.Errorf("no readiness ping: %v", fd.execs)
	}
	if !strings.Contains(joined, "/root/.my.cnf") {
		t.Errorf("client config not written: %v", fd.execs)
	}
}

func TestMariaDBEngine_ProvisionCommand(t *testing.T) {
	fd := &fakeDocker{}
	e := mariadbEngine(fd, t.TempDir())
	if err := e.Provision(context.Background(), "blog_abc", "blog_abc_u", "pw123"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	cmd := fd.execs[len(fd.execs)-1]
	for _, want := range []string{"CREATE DATABASE IF NOT EXISTS blog_abc", "CREATE USER IF NOT EXISTS 'blog_abc_u'@'%'", "GRANT ALL PRIVILEGES ON blog_abc.*"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("provision command missing %q:\n%s", want, cmd)
		}
	}
	// No backticks — they'd trigger command substitution in the container shell.
	if strings.Contains(cmd, "`") {
		t.Errorf("provision command must not contain backticks: %s", cmd)
	}
}

func TestMariaDBEngine_ConnStringAndBackup(t *testing.T) {
	e := mariadbEngine(&fakeDocker{}, t.TempDir())
	if got := e.ConnString("blog_abc", "blog_abc_u", "pw"); got != "mysql://blog_abc_u:pw@vac-mariadb:3306/blog_abc" {
		t.Errorf("conn string = %q", got)
	}
	if got := e.DefaultBackupCommand("blog_abc"); got != "mariadb-dump blog_abc" {
		t.Errorf("backup command = %q", got)
	}
	if !e.Shared() || e.FootprintMB() == 0 {
		t.Errorf("mariadb should be shared + carry a footprint")
	}
}
