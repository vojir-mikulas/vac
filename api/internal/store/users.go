package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
	TOTPEnabled  bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID         string
	UserID     string
	IPAddress  *netip.Addr
	UserAgent  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	PreAuth    bool
	// StepUpVerifiedAt is the last time this session re-proved 2FA for a
	// sensitive action. nil means never. Destructive routes require it to be
	// recent (see auth.StepUpTTL).
	StepUpVerifiedAt *time.Time
}

// --- users ---

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id, username, password_hash, totp_enabled, created_at, updated_at
	`, username, passwordHash).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, totp_enabled, created_at, updated_at
		FROM users WHERE username = $1
	`, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, totp_enabled, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UpdateUserPassword rotates the password hash. Caller is expected to revoke
// existing sessions afterwards so a stolen cookie cannot survive a reset.
func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2
	`, passwordHash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- sessions ---

func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, ip *netip.Addr, ua string, expiresAt time.Time, preAuth bool) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, ip_address, user_agent, expires_at, pre_auth)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, ip_address, user_agent, created_at, expires_at, last_seen_at, pre_auth
	`, userID, tokenHash, ip, ua, expiresAt, preAuth).Scan(&sess.ID, &sess.UserID, &sess.IPAddress, &sess.UserAgent, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.PreAuth)
	return sess, err
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, ip_address, user_agent, created_at, expires_at, last_seen_at, pre_auth, stepup_verified_at
		FROM sessions WHERE token_hash = $1
	`, tokenHash).Scan(&sess.ID, &sess.UserID, &sess.IPAddress, &sess.UserAgent, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.PreAuth, &sess.StepUpVerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return sess, err
}

func (s *Store) UpdateSessionLastSeen(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE id = $2`, at, id)
	return err
}

// TouchSessionStepUp stamps stepup_verified_at = NOW() on the session, marking
// it as having freshly re-proved 2FA. Returns ErrNotFound if the session is
// gone (e.g. revoked between the challenge and the stamp).
func (s *Store) TouchSessionStepUp(ctx context.Context, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET stepup_verified_at = $1 WHERE id = $2`, at, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

// ListSessionsForUser returns the user's active full sessions, most recently
// seen first. Pre-auth (TOTP-pending) and expired rows are intentionally
// excluded — the user-facing list should only show real, live devices.
func (s *Store) ListSessionsForUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, ip_address, user_agent, created_at, expires_at, last_seen_at, pre_auth
		FROM sessions
		WHERE user_id = $1 AND pre_auth = FALSE AND expires_at > NOW()
		ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.IPAddress, &sess.UserAgent, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.PreAuth); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeAllSessionsForUser deletes every session (full + pre-auth) for the
// user. Used after a password reset so a leaked cookie cannot survive the
// rotation.
func (s *Store) RevokeAllSessionsForUser(ctx context.Context, userID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokeOtherSessionsForUser deletes every full session owned by userID
// except exceptID. Returns the number of rows deleted so the caller can
// surface "N other devices signed out".
func (s *Store) RevokeOtherSessionsForUser(ctx context.Context, userID, exceptID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE user_id = $1 AND id <> $2 AND pre_auth = FALSE
	`, userID, exceptID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- totp ---

// SetUserTOTPSecret stores an encrypted TOTP secret as pending. totp_enabled
// stays FALSE until EnableUserTOTP is called with a valid code.
func (s *Store) SetUserTOTPSecret(ctx context.Context, userID string, encSecret []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		SET totp_secret = $1, totp_enabled = FALSE, totp_recovery_codes = NULL, updated_at = NOW()
		WHERE id = $2
	`, encSecret, userID)
	return err
}

// GetUserTOTPSecret returns the encrypted secret stored on the user. Returns
// ErrNotFound when no secret has been set (TOTP setup not started).
func (s *Store) GetUserTOTPSecret(ctx context.Context, userID string) ([]byte, error) {
	var secret []byte
	err := s.pool.QueryRow(ctx, `SELECT totp_secret FROM users WHERE id = $1`, userID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 {
		return nil, ErrNotFound
	}
	return secret, nil
}

// EnableUserTOTP flips totp_enabled to TRUE and stores the hashed recovery
// codes. Caller is expected to have just verified a TOTP code against the
// pending secret.
func (s *Store) EnableUserTOTP(ctx context.Context, userID string, recoveryHashes []string) error {
	payload, err := json.Marshal(recoveryHashes)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE users
		SET totp_enabled = TRUE, totp_recovery_codes = $1, updated_at = NOW()
		WHERE id = $2
	`, payload, userID)
	return err
}

// DisableUserTOTP clears the secret, the enabled flag, and any recovery codes.
func (s *Store) DisableUserTOTP(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		SET totp_secret = NULL, totp_enabled = FALSE, totp_recovery_codes = NULL, updated_at = NOW()
		WHERE id = $1
	`, userID)
	return err
}

// ConsumeRecoveryCode removes hexHash from the user's recovery code list and
// returns true if it was present. The update is done atomically via a single
// UPDATE so concurrent uses cannot both succeed.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, hexHash string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET totp_recovery_codes = COALESCE(totp_recovery_codes, '[]'::jsonb) - $2
		WHERE id = $1
		  AND totp_recovery_codes @> to_jsonb(ARRAY[$2]::text[])
	`, userID, hexHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
