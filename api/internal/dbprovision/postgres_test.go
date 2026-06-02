package dbprovision

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakePG struct {
	sqls []string
	err  error
}

func (f *fakePG) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.sqls = append(f.sqls, sql)
	return pgconn.CommandTag{}, f.err
}

type fakeNet struct {
	network, container, alias string
	called                    bool
}

func (f *fakeNet) NetworkConnect(_ context.Context, network, container, alias string) error {
	f.called = true
	f.network, f.container, f.alias = network, container, alias
	return nil
}

func TestPostgresEngine_Provision(t *testing.T) {
	pg := &fakePG{}
	e := NewPostgresEngine(pg, &fakeNet{}, Config{EdgeNetwork: "vac-edge"})
	if err := e.Provision(context.Background(), "blog_abc123", "blog_abc123_u", "Secr3tPass"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(pg.sqls) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(pg.sqls), pg.sqls)
	}
	if !strings.HasPrefix(pg.sqls[0], "CREATE ROLE ") || !strings.Contains(pg.sqls[0], "'Secr3tPass'") {
		t.Errorf("create role wrong: %q", pg.sqls[0])
	}
	if !strings.HasPrefix(pg.sqls[1], "CREATE DATABASE ") || !strings.Contains(pg.sqls[1], "OWNER") {
		t.Errorf("create database wrong: %q", pg.sqls[1])
	}
	// Identifiers must be double-quoted (pgx Identifier.Sanitize).
	if !strings.Contains(pg.sqls[1], `"blog_abc123"`) {
		t.Errorf("db identifier not quoted: %q", pg.sqls[1])
	}
}

func TestPostgresEngine_Deprovision(t *testing.T) {
	pg := &fakePG{}
	e := NewPostgresEngine(pg, &fakeNet{}, Config{})
	if err := e.Deprovision(context.Background(), "blog_abc123", "blog_abc123_u"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	if len(pg.sqls) != 2 {
		t.Fatalf("expected 2 statements, got %v", pg.sqls)
	}
	if !strings.Contains(pg.sqls[0], "DROP DATABASE IF EXISTS") || !strings.Contains(pg.sqls[0], "FORCE") {
		t.Errorf("drop database wrong: %q", pg.sqls[0])
	}
	if !strings.Contains(pg.sqls[1], "DROP ROLE IF EXISTS") {
		t.Errorf("drop role wrong: %q", pg.sqls[1])
	}
}

func TestPostgresEngine_ConnStringAndBackup(t *testing.T) {
	e := NewPostgresEngine(&fakePG{}, &fakeNet{}, Config{})
	conn := e.ConnString("blog_abc", "blog_abc_u", "pw")
	if conn != "postgres://blog_abc_u:pw@vac-db:5432/blog_abc?sslmode=disable" {
		t.Errorf("conn string = %q", conn)
	}
	if cmd := e.DefaultBackupCommand("blog_abc"); cmd != "pg_dump -U vac blog_abc" {
		t.Errorf("backup command = %q", cmd)
	}
	if e.BackupContainer() != "vac-db" {
		t.Errorf("backup container = %q", e.BackupContainer())
	}
	if e.Shared() || e.FootprintMB() != 0 {
		t.Errorf("postgres should be free + non-shared")
	}
}

func TestPostgresEngine_EnsureRunningAttachesEdge(t *testing.T) {
	net := &fakeNet{}
	e := NewPostgresEngine(&fakePG{}, net, Config{EdgeNetwork: "vac-edge"})
	if err := e.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if !net.called || net.network != "vac-edge" || net.container != "vac-db" || net.alias != "vac-db" {
		t.Errorf("edge attach wrong: %+v", net)
	}
}
