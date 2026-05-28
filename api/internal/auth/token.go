package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TokenPrefix is the literal "vac_" that every API token starts with. Easy to
// grep for in leaks and unambiguous so we can refuse non-VAC bearer values
// without a DB roundtrip.
const TokenPrefix = "vac_"

// ErrTokenExpired is returned by TokenManager.Lookup when the token row
// carries an expires_at in the past.
var ErrTokenExpired = errors.New("auth: api token expired")

// TokenManager mints, validates, and revokes API tokens. Raw tokens leave
// this package exactly twice — at Create time and as user input on Lookup —
// and never get persisted; the DB holds only the SHA-256 hash.
type TokenManager struct {
	store *store.Store
}

func NewTokenManager(s *store.Store) *TokenManager {
	return &TokenManager{store: s}
}

// Create mints a new token for userID with the given human-readable name.
// expiresAt may be nil for "no expiry". The returned raw token is shown to
// the user once and cannot be recovered afterward.
func (m *TokenManager) Create(ctx context.Context, userID, name string, expiresAt *time.Time) (raw string, t store.APIToken, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", store.APIToken{}, fmt.Errorf("auth: token rand: %w", err)
	}
	raw = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(raw))

	t, err = m.store.CreateAPIToken(ctx, userID, name, hash[:], expiresAt)
	if err != nil {
		return "", store.APIToken{}, err
	}
	return raw, t, nil
}

// Lookup resolves a raw Bearer token to (token row, user). Returns
// store.ErrNotFound for unknown / malformed tokens and ErrTokenExpired for
// past-due ones. Best-effort updates last_used_at on success.
func (m *TokenManager) Lookup(ctx context.Context, raw string) (store.APIToken, store.User, error) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return store.APIToken{}, store.User{}, store.ErrNotFound
	}
	hash := sha256.Sum256([]byte(raw))
	tok, err := m.store.GetAPITokenByHash(ctx, hash[:])
	if err != nil {
		return store.APIToken{}, store.User{}, err
	}
	if tok.ExpiresAt != nil && time.Now().After(*tok.ExpiresAt) {
		return store.APIToken{}, store.User{}, ErrTokenExpired
	}
	user, err := m.store.GetUserByID(ctx, tok.UserID)
	if err != nil {
		return store.APIToken{}, store.User{}, err
	}
	_ = m.store.UpdateAPITokenLastUsed(ctx, tok.ID, time.Now())
	return tok, user, nil
}

// Revoke deletes a token. user_id is enforced at the SQL layer so users can
// only revoke their own tokens, not someone else's.
func (m *TokenManager) Revoke(ctx context.Context, userID, id string) error {
	return m.store.RevokeAPIToken(ctx, userID, id)
}
