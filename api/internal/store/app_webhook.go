package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetAppWebhookSecret returns the sealed per-app webhook secret, or (nil, nil)
// when none is set (webhooks disabled for the app). The store only ever sees
// ciphertext — the caller seals/opens with crypto.Box. Kept off the App struct
// so the secret never rides along on ordinary app reads.
func (s *Store) GetAppWebhookSecret(ctx context.Context, appID string) ([]byte, error) {
	var enc []byte
	err := s.pool.QueryRow(ctx,
		`SELECT webhook_secret_enc FROM apps WHERE id = $1`, appID).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return enc, err
}

// SetAppWebhookSecret stores (or, with nil, clears) the sealed webhook secret.
func (s *Store) SetAppWebhookSecret(ctx context.Context, appID string, enc []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET webhook_secret_enc = $2, updated_at = NOW() WHERE id = $1`, appID, enc)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
