package sshkey

import (
	"context"
	"errors"
	"fmt"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrEncryptionUnavailable is returned by Mint when VAC_MASTER_KEY is unset
// — without a crypto box we refuse to persist a private key.
var ErrEncryptionUnavailable = errors.New("sshkey: encryption not configured (VAC_MASTER_KEY missing)")

// keyStore is the slice of *store.Store this package actually uses.
// Defined as an interface so tests can stand in a fake.
type keyStore interface {
	UpsertSSHKey(ctx context.Context, appID, publicKey string, privateKey []byte) (store.SSHKey, error)
	GetSSHKeyForApp(ctx context.Context, appID string) (store.SSHKey, error)
	DeleteSSHKeyForApp(ctx context.Context, appID string) error
}

// Manager owns SSH key lifecycle for the deploy pipeline. It is the only
// place that calls crypto.Box for sshkey material.
type Manager struct {
	store keyStore
	box   *crypto.Box
}

func NewManager(s *store.Store, box *crypto.Box) *Manager {
	return &Manager{store: s, box: box}
}

// Mint generates a fresh ED25519 keypair and seals the private half. If a
// key already exists for the app, it is replaced (ON CONFLICT DO UPDATE on
// the unique app_id).
func (m *Manager) Mint(ctx context.Context, app store.App) (store.SSHKey, error) {
	if m.box == nil {
		return store.SSHKey{}, ErrEncryptionUnavailable
	}
	kp, err := Generate(app.Slug)
	if err != nil {
		return store.SSHKey{}, err
	}
	sealed, err := m.box.Seal(kp.PrivatePEM)
	if err != nil {
		return store.SSHKey{}, fmt.Errorf("sshkey: seal: %w", err)
	}
	return m.store.UpsertSSHKey(ctx, app.ID, kp.PublicLine, sealed)
}

// Get returns the stored key. The private key in the returned struct is
// still sealed — call OpenPrivateKey to decrypt.
func (m *Manager) Get(ctx context.Context, appID string) (store.SSHKey, error) {
	return m.store.GetSSHKeyForApp(ctx, appID)
}

// OpenPrivateKey returns the plaintext PEM bytes for the app's stored key.
// Callers (M4 test-connection, M7 pipeline) write these to a temp file
// referenced by GIT_SSH_COMMAND.
func (m *Manager) OpenPrivateKey(ctx context.Context, appID string) ([]byte, error) {
	if m.box == nil {
		return nil, ErrEncryptionUnavailable
	}
	k, err := m.store.GetSSHKeyForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	pem, err := m.box.Open(k.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("sshkey: open: %w", err)
	}
	return pem, nil
}

func (m *Manager) Delete(ctx context.Context, appID string) error {
	return m.store.DeleteSSHKeyForApp(ctx, appID)
}

// Fingerprint computes the SHA256 fingerprint of a stored public-key line.
// Returns "" on parse failure — the caller can show a placeholder rather
// than blocking the response.
func Fingerprint(publicLine string) string {
	return fingerprintForLine(publicLine)
}
