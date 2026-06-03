// Package auth handles credentials, sessions, TOTP, and API tokens.
package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is high enough that ~250ms per Hash on a recent server CPU is
// acceptable for an interactive login, while making offline cracking expensive.
const bcryptCost = 12

// MaxPasswordBytes is bcrypt's hard input limit: it only considers the first 72
// bytes of the password and rejects longer inputs (golang.org/x/crypto/bcrypt
// returns ErrPasswordTooLong rather than silently truncating). Callers should
// validate against this BEFORE hashing so an over-long password yields a clear
// 400 instead of a generic hashing error — and so nobody is misled into thinking
// the ignored tail of a >72-byte password adds strength. It is a byte count, not
// a rune count, so multi-byte characters consume the budget faster.
const MaxPasswordBytes = 72

// HashPassword returns the bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword returns nil if the bcrypt hash matches the plaintext password,
// or bcrypt.ErrMismatchedHashAndPassword otherwise.
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
