package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetServiceGuestAccessCode returns the sealed shared access code for one
// service, or (nil, nil) when none is set (guest access disabled — only
// operators pass the login gate for it). The store only ever sees ciphertext;
// the caller seals/opens with crypto.Box. Mirrors GetAppWebhookSecret, but per
// service so each guarded container is shared independently.
func (s *Store) GetServiceGuestAccessCode(ctx context.Context, appID, name string) ([]byte, error) {
	var enc []byte
	err := s.pool.QueryRow(ctx,
		`SELECT guest_access_code_enc FROM services WHERE app_id = $1 AND service_name = $2`,
		appID, name).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return enc, err
}

// SetServiceGuestAccessCode stores (or, with nil, clears) the sealed shared
// access code for one service.
func (s *Store) SetServiceGuestAccessCode(ctx context.Context, appID, name string, enc []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE services SET guest_access_code_enc = $3, updated_at = NOW()
		 WHERE app_id = $1 AND service_name = $2`, appID, name, enc)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
