package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIToken is a programmatic credential. The raw token is only ever returned
// from the Create call; the DB holds the SHA-256 hash.
type APIToken struct {
	ID         string
	UserID     string
	Name       string
	LastUsedAt *time.Time
	CreatedAt  time.Time
	ExpiresAt  *time.Time
}

func (s *Store) CreateAPIToken(ctx context.Context, userID, name string, tokenHash []byte, expiresAt *time.Time) (APIToken, error) {
	var t APIToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, last_used_at, created_at, expires_at
	`, userID, name, tokenHash, expiresAt).Scan(&t.ID, &t.UserID, &t.Name, &t.LastUsedAt, &t.CreatedAt, &t.ExpiresAt)
	return t, err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash []byte) (APIToken, error) {
	var t APIToken
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, name, last_used_at, created_at, expires_at
		FROM api_tokens WHERE token_hash = $1
	`, tokenHash).Scan(&t.ID, &t.UserID, &t.Name, &t.LastUsedAt, &t.CreatedAt, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	return t, err
}

func (s *Store) UpdateAPITokenLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, at, id)
	return err
}

func (s *Store) ListAPITokensForUser(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, last_used_at, created_at, expires_at
		FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.LastUsedAt, &t.CreatedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken deletes the token. Returns ErrNotFound if id+userID doesn't
// match anything — the user_id pin prevents cross-user revocation.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
