package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// RegistryAuth is the private-registry credential set for an image-sourced app.
// It is sealed as JSON (crypto.Box) before storage — the store never sees this
// shape, only the resulting ciphertext. The pipeline opens it to `docker login`
// before pulling a private image; the API never returns the password.
type RegistryAuth struct {
	// Registry is the registry host (e.g. ghcr.io). Empty = Docker Hub default.
	Registry string `json:"registry"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// GetAppRegistryAuth returns the sealed per-app registry credentials, or
// (nil, nil) when none are set (public image). The store only ever sees
// ciphertext — the caller seals/opens with crypto.Box. Kept off the App struct
// so the credentials never ride along on ordinary app reads.
func (s *Store) GetAppRegistryAuth(ctx context.Context, appID string) ([]byte, error) {
	var enc []byte
	err := s.pool.QueryRow(ctx,
		`SELECT registry_auth_enc FROM apps WHERE id = $1`, appID).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return enc, err
}

// SetAppRegistryAuth stores (or, with nil, clears) the sealed registry creds.
func (s *Store) SetAppRegistryAuth(ctx context.Context, appID string, enc []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE apps SET registry_auth_enc = $2, updated_at = NOW() WHERE id = $1`, appID, enc)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
