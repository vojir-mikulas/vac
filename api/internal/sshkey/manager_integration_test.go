//go:build integration

package sshkey_test

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gossh "golang.org/x/crypto/ssh"

	vaccrypto "github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func setupStore(t *testing.T) *store.Store {
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
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return store.New(pool)
}

func newBox(t *testing.T) *vaccrypto.Box {
	t.Helper()
	key := make([]byte, vaccrypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := vaccrypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestManager_MintGetOpenRotateDelete(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	mgr := sshkey.NewManager(s, newBox(t))

	a, err := s.CreateApp(ctx, "Demo", "demo-mgr", "git@github.com:vojir-mikulas/demo.git", "main", "compose.yaml")
	if err != nil {
		t.Fatal(err)
	}

	first, err := mgr.Mint(ctx, a)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if first.PublicKey == "" || len(first.PrivateKey) == 0 {
		t.Errorf("Mint returned empty key fields: %+v", first)
	}

	again, err := mgr.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.ID != first.ID || again.PublicKey != first.PublicKey {
		t.Errorf("Get did not return the just-minted row")
	}

	// OpenPrivateKey round-trips the sealed bytes through crypto.Box and
	// the result must be a valid SSH private key.
	pem, err := mgr.OpenPrivateKey(ctx, a.ID)
	if err != nil {
		t.Fatalf("OpenPrivateKey: %v", err)
	}
	if _, err := gossh.ParsePrivateKey(pem); err != nil {
		t.Fatalf("decrypted PEM not parseable: %v", err)
	}

	// Rotate — same row, different fingerprint.
	second, err := mgr.Mint(ctx, a)
	if err != nil {
		t.Fatalf("Mint rotate: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("rotation changed row id: %s → %s", first.ID, second.ID)
	}
	if second.PublicKey == first.PublicKey {
		t.Errorf("rotation produced identical public key")
	}

	if err := mgr.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := mgr.Get(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("post-delete Get: want ErrNotFound, got %v", err)
	}
}

func TestManager_MintRefusesWithoutBox(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	mgr := sshkey.NewManager(s, nil)

	a, err := s.CreateApp(ctx, "Demo", "demo-nobox", "git@github.com:x/y.git", "main", "compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Mint(ctx, a); !errors.Is(err, sshkey.ErrEncryptionUnavailable) {
		t.Errorf("Mint without box: want ErrEncryptionUnavailable, got %v", err)
	}
}
