// Package auth handles credentials, sessions, TOTP, and API tokens.
package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is high enough that ~250ms per Hash on a recent server CPU is
// acceptable for an interactive login, while making offline cracking expensive.
const bcryptCost = 12

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
