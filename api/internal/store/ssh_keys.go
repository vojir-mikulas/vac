package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// SSHKey is the ED25519 deploy-key pair associated 1:1 with an app. The
// private key is sealed with crypto.Box (AES-256-GCM, VAC_MASTER_KEY).
type SSHKey struct {
	ID         string
	AppID      string
	PublicKey  string
	PrivateKey []byte
	CreatedAt  time.Time
}

// UpsertSSHKey inserts a new key or replaces the existing one for the app.
// "Regenerate" is the same call — the UNIQUE on app_id makes ON CONFLICT
// the natural shape.
func (s *Store) UpsertSSHKey(ctx context.Context, appID, publicKey string, privateKey []byte) (SSHKey, error) {
	var k SSHKey
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ssh_keys (app_id, public_key, private_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (app_id) DO UPDATE
			SET public_key  = EXCLUDED.public_key,
			    private_key = EXCLUDED.private_key,
			    created_at  = NOW()
		RETURNING id, app_id, public_key, private_key, created_at
	`, appID, publicKey, privateKey).Scan(&k.ID, &k.AppID, &k.PublicKey, &k.PrivateKey, &k.CreatedAt)
	return k, err
}

func (s *Store) GetSSHKeyForApp(ctx context.Context, appID string) (SSHKey, error) {
	var k SSHKey
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, public_key, private_key, created_at
		FROM ssh_keys WHERE app_id = $1
	`, appID).Scan(&k.ID, &k.AppID, &k.PublicKey, &k.PrivateKey, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SSHKey{}, ErrNotFound
	}
	return k, err
}

func (s *Store) DeleteSSHKeyForApp(ctx context.Context, appID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM ssh_keys WHERE app_id = $1`, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
