package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrExpired is returned when a session token is valid in shape but past its
// expires_at timestamp.
var ErrExpired = errors.New("auth: session expired")

// SessionManager creates and validates session tokens. Raw tokens are returned
// once at creation (for the cookie) and never persisted — only the SHA-256
// hash lives in the DB. A leaked DB dump cannot be used to hijack live sessions.
type SessionManager struct {
	store       *store.Store
	ttl         time.Duration
	extendedTTL time.Duration
}

func NewSessionManager(s *store.Store, ttl, extendedTTL time.Duration) *SessionManager {
	return &SessionManager{store: s, ttl: ttl, extendedTTL: extendedTTL}
}

// TTL returns the session lifetime that Create would use for the extended flag.
func (m *SessionManager) TTL(extended bool) time.Duration {
	if extended {
		return m.extendedTTL
	}
	return m.ttl
}

// Create issues a new session and returns the raw token (for the cookie) along
// with the persisted session row. The raw token is not stored anywhere.
func (m *SessionManager) Create(ctx context.Context, userID string, ip *netip.Addr, ua string, extended bool) (rawToken string, sess store.Session, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", store.Session{}, fmt.Errorf("auth: token rand: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256(raw)

	sess, err = m.store.CreateSession(ctx, userID, hash[:], ip, ua, time.Now().Add(m.TTL(extended)))
	if err != nil {
		return "", store.Session{}, err
	}
	return token, sess, nil
}

// Lookup resolves a raw cookie token to (session, user). Returns
// store.ErrNotFound for unknown tokens and ErrExpired for past-due sessions.
func (m *SessionManager) Lookup(ctx context.Context, rawToken string) (store.Session, store.User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(rawToken)
	if err != nil {
		return store.Session{}, store.User{}, store.ErrNotFound
	}
	hash := sha256.Sum256(raw)
	sess, err := m.store.GetSessionByTokenHash(ctx, hash[:])
	if err != nil {
		return store.Session{}, store.User{}, err
	}
	if time.Now().After(sess.ExpiresAt) {
		return store.Session{}, store.User{}, ErrExpired
	}
	user, err := m.store.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return store.Session{}, store.User{}, err
	}
	// Best-effort: bump last_seen_at, but don't fail the lookup if the write errors.
	_ = m.store.UpdateSessionLastSeen(ctx, sess.ID, time.Now())
	return sess, user, nil
}

func (m *SessionManager) Revoke(ctx context.Context, sessionID string) error {
	return m.store.RevokeSession(ctx, sessionID)
}
