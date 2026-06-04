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

// ErrPreAuth is returned when a normal Lookup hits a pre-auth (TOTP-pending)
// session. Callers should treat the request as anonymous.
var ErrPreAuth = errors.New("auth: session is pre-auth only")

// ErrNotPreAuth is returned by LookupPreAuth when the token resolves to a
// full session rather than a pending one. The caller should reject — using a
// full session token to satisfy the TOTP step would defeat 2FA.
var ErrNotPreAuth = errors.New("auth: session is not pre-auth")

// PreAuthTTL is how long a TOTP-pending session is valid for. Short enough
// that a stolen pre-auth cookie cannot be sat on, long enough for the user
// to fetch their authenticator app.
const PreAuthTTL = 10 * time.Minute

// StepUpTTL is how long a step-up 2FA proof stays fresh. After re-verifying,
// the user may perform sensitive actions for this window without being
// re-challenged; past it, the next destructive request demands a new code.
const StepUpTTL = 5 * time.Minute

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

// Create issues a new full session and returns the raw token (for the cookie)
// along with the persisted session row. The raw token is not stored anywhere.
func (m *SessionManager) Create(ctx context.Context, userID string, ip *netip.Addr, ua string, extended bool) (rawToken string, sess store.Session, err error) {
	return m.create(ctx, userID, ip, ua, m.TTL(extended), false)
}

// CreatePreAuth issues a short-lived session that satisfies only the TOTP
// step of login. Auth middleware refuses it — only the /api/auth/totp handler
// accepts it via LookupPreAuth.
func (m *SessionManager) CreatePreAuth(ctx context.Context, userID string, ip *netip.Addr, ua string) (rawToken string, sess store.Session, err error) {
	return m.create(ctx, userID, ip, ua, PreAuthTTL, true)
}

func (m *SessionManager) create(ctx context.Context, userID string, ip *netip.Addr, ua string, ttl time.Duration, preAuth bool) (string, store.Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", store.Session{}, fmt.Errorf("auth: token rand: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256(raw)

	sess, err := m.store.CreateSession(ctx, userID, hash[:], ip, ua, time.Now().Add(ttl), preAuth)
	if err != nil {
		return "", store.Session{}, err
	}
	return token, sess, nil
}

// Lookup resolves a raw cookie token to (session, user). Returns
// store.ErrNotFound for unknown tokens, ErrExpired for past-due sessions, and
// ErrPreAuth for sessions that have not cleared the TOTP step.
func (m *SessionManager) Lookup(ctx context.Context, rawToken string) (store.Session, store.User, error) {
	sess, user, err := m.lookup(ctx, rawToken)
	if err != nil {
		return store.Session{}, store.User{}, err
	}
	if sess.PreAuth {
		return store.Session{}, store.User{}, ErrPreAuth
	}
	// Best-effort: bump last_seen_at, but don't fail the lookup if the write errors.
	_ = m.store.UpdateSessionLastSeen(ctx, sess.ID, time.Now())
	return sess, user, nil
}

// LookupPreAuth is the dual of Lookup for the TOTP step: it accepts only
// pending sessions and rejects full ones.
func (m *SessionManager) LookupPreAuth(ctx context.Context, rawToken string) (store.Session, store.User, error) {
	sess, user, err := m.lookup(ctx, rawToken)
	if err != nil {
		return store.Session{}, store.User{}, err
	}
	if !sess.PreAuth {
		return store.Session{}, store.User{}, ErrNotPreAuth
	}
	return sess, user, nil
}

func (m *SessionManager) lookup(ctx context.Context, rawToken string) (store.Session, store.User, error) {
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
	return sess, user, nil
}

func (m *SessionManager) Revoke(ctx context.Context, sessionID string) error {
	return m.store.RevokeSession(ctx, sessionID)
}

// MarkStepUp stamps the session as having freshly re-proved 2FA. Callers invoke
// it after a successful step-up TOTP / recovery-code check; RequireStepUp then
// honours the session for StepUpTTL.
func (m *SessionManager) MarkStepUp(ctx context.Context, sessionID string) error {
	return m.store.TouchSessionStepUp(ctx, sessionID, time.Now())
}

// StepUpFresh reports whether sess re-proved 2FA within StepUpTTL of now.
func StepUpFresh(sess store.Session) bool {
	if sess.StepUpVerifiedAt == nil {
		return false
	}
	return time.Since(*sess.StepUpVerifiedAt) < StepUpTTL
}
