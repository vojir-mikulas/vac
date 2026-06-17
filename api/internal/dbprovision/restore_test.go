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
