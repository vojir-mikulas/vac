package dbprovision

import (
	"log/slog"
	"strings"
	"testing"
)

func TestPostgresEngine_MatchAndRestore(t *testing.T) {
	e := NewPostgresEngine(nil, nil, Config{})
	cmd := e.DefaultBackupCommand("blog_abc123")
	db, ok := e.MatchBackupCommand(cmd)
	if !ok || db != "blog_abc123" {
		t.Fatalf("MatchBackupCommand(%q) = %q,%v", cmd, db, ok)
	}
	// A custom command isn't recognized.
	if _, ok := e.MatchBackupCommand("pg_dump -Fc -U someone otherdb"); ok {
		t.Error("custom command should not match")
	}
	if _, ok := e.MatchBackupCommand("pg_dump -U vac db; rm -rf /"); ok {
		t.Error("unsafe identifier should not match")
	}
	restore := e.RestoreCommand("blog_abc123")
	if !strings.Contains(restore, "psql -U vac -d blog_abc123") || !strings.Contains(restore, "DROP SCHEMA") {
		t.Errorf("RestoreCommand = %q", restore)
	}
}

func TestMariaDBEngine_MatchAndRestore(t *testing.T) {
	e := NewMariaDBEngine(nil, Config{MasterKey: []byte("k")})
	cmd := e.DefaultBackupCommand("shop_xy")
	db, ok := e.MatchBackupCommand(cmd)
	if !ok || db != "shop_xy" {
		t.Fatalf("MatchBackupCommand(%q) = %q,%v", cmd, db, ok)
	}
	if got := e.RestoreCommand("shop_xy"); got != "mariadb shop_xy" {
		t.Errorf("RestoreCommand = %q, want mariadb shop_xy", got)
	}
}

func TestEngines_VerifyRestoreCommand(t *testing.T) {
	pg := NewPostgresEngine(nil, nil, Config{}).VerifyRestoreCommand("vac_verify_abc")
	// Creates the scratch DB, replays into it, and always drops it preserving rc.
	for _, want := range []string{
		"CREATE DATABASE vac_verify_abc",
		"psql -U vac -d vac_verify_abc -v ON_ERROR_STOP=1",
		"DROP DATABASE IF EXISTS vac_verify_abc",
		"exit $rc",
	} {
		if !strings.Contains(pg, want) {
			t.Errorf("postgres verify cmd missing %q: %s", want, pg)
		}
	}
	// Critically, it must NOT touch the real database — only the scratch name.
	if strings.Contains(pg, "DROP SCHEMA") {
		t.Errorf("verify must not reset a live schema: %s", pg)
	}

	maria := NewMariaDBEngine(nil, Config{MasterKey: []byte("k")}).VerifyRestoreCommand("vac_verify_xy")
	for _, want := range []string{"CREATE DATABASE vac_verify_xy", "mariadb vac_verify_xy", "DROP DATABASE IF EXISTS vac_verify_xy", "exit $rc"} {
		if !strings.Contains(maria, want) {
			t.Errorf("mariadb verify cmd missing %q: %s", want, maria)
		}
	}
}

func TestProvisioner_VerifyCommandFor(t *testing.T) {
	p := New(nil, nil, nil, nil, Config{MasterKey: []byte("k")}, slog.Default())
	if cmd, ok := p.VerifyCommandFor("pg_dump -U vac blog_abc", "vac_verify_z"); !ok ||
		!strings.Contains(cmd, "CREATE DATABASE vac_verify_z") {
		t.Errorf("postgres verify = %q,%v", cmd, ok)
	}
	if _, ok := p.VerifyCommandFor("pg_dump -U $POSTGRES_USER $POSTGRES_DB", "vac_verify_z"); ok {
		t.Error("custom command should be refused for verification")
	}
}

func TestProvisioner_RestoreCommandFor(t *testing.T) {
	p := New(nil, nil, nil, nil, Config{MasterKey: []byte("k")}, slog.Default())
	// Postgres default → recognized.
	if cmd, ok := p.RestoreCommandFor("pg_dump -U vac blog_abc"); !ok || !strings.Contains(cmd, "psql -U vac -d blog_abc") {
		t.Errorf("postgres restore = %q,%v", cmd, ok)
	}
	// MariaDB default → recognized.
	if cmd, ok := p.RestoreCommandFor("mariadb-dump shop_xy"); !ok || cmd != "mariadb shop_xy" {
		t.Errorf("mariadb restore = %q,%v", cmd, ok)
	}
	// Hand-authored command → refused.
	if _, ok := p.RestoreCommandFor("pg_dump -U $POSTGRES_USER $POSTGRES_DB"); ok {
		t.Error("custom command should be refused")
	}
}
