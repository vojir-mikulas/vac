//go:build integration

package store_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func setup(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	pgC, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("vac"),
		postgres.WithUsername("vac"),
		postgres.WithPassword("vac"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker / postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(ctx) })

	url, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	return store.New(pool)
}

func randomToken(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

func TestUsersCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	count, err := s.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers empty: %v", err)
	}
	if count != 0 {
		t.Errorf("fresh db count = %d, want 0", count)
	}

	created, err := s.CreateUser(ctx, "alice", "hash$bcrypt")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == "" || created.Username != "alice" {
		t.Fatalf("unexpected created user: %+v", created)
	}
	if created.TOTPEnabled {
		t.Error("totp_enabled should default to false")
	}

	fetched, err := s.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("id mismatch: %s vs %s", fetched.ID, created.ID)
	}

	byID, err := s.GetUserByID(ctx, created.ID)
	if err != nil || byID.Username != "alice" {
		t.Errorf("GetUserByID = %+v, %v", byID, err)
	}

	if _, err := s.GetUserByUsername(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing user, got %v", err)
	}

	count, _ = s.CountUsers(ctx)
	if count != 1 {
		t.Errorf("count after insert = %d, want 1", count)
	}
}

func TestSessionsCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "bob", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	expires := time.Now().Add(7 * 24 * time.Hour)
	tokenHash := randomToken(t)
	sess, err := s.CreateSession(ctx, u.ID, tokenHash, nil, "go-test", expires, false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.UserID != u.ID || sess.UserAgent != "go-test" {
		t.Errorf("unexpected session: %+v", sess)
	}

	got, err := s.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("session id mismatch")
	}

	if _, err := s.GetSessionByTokenHash(ctx, []byte("nope-not-a-real-hash-padding-32b")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing session, got %v", err)
	}

	before := got.LastSeenAt
	time.Sleep(20 * time.Millisecond)
	if err := s.UpdateSessionLastSeen(ctx, sess.ID, time.Now()); err != nil {
		t.Fatalf("UpdateSessionLastSeen: %v", err)
	}
	refreshed, _ := s.GetSessionByTokenHash(ctx, tokenHash)
	if !refreshed.LastSeenAt.After(before) {
		t.Errorf("last_seen_at not updated: before=%v after=%v", before, refreshed.LastSeenAt)
	}

	list, err := s.ListSessionsForUser(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSessionsForUser = %v, %v", list, err)
	}

	if err := s.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, tokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session should be gone after revoke, got %v", err)
	}
}
