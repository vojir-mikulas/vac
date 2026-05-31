package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// setupTokenFile is the on-disk location for the first-boot setup token,
// relative to WorkDir. Its presence proves whoever calls /api/setup/admin has
// filesystem access on the host — the same trust we already require for the
// master key. Deleted as soon as the admin account is created.
const setupTokenFile = "setup.token"

// ErrSetupTokenMissing means there is no setup token on disk to consume. The
// caller should refuse to bootstrap an admin: either someone already did, or
// the operator needs to restart vac-api to regenerate the token.
var ErrSetupTokenMissing = errors.New("auth: setup token not present")

// ErrSetupTokenMismatch is returned by ConsumeSetupToken when the supplied
// token does not match the one on disk. Constant-time comparison.
var ErrSetupTokenMismatch = errors.New("auth: setup token mismatch")

// EnsureSetupToken returns the current first-boot token, generating and
// persisting one if none exists. The file is written with 0600 permissions so
// only the vac-api process user can read it.
func EnsureSetupToken(workDir string) (string, error) {
	if existing, err := ReadSetupToken(workDir); err == nil && existing != "" {
		return existing, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: setup token rand: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", fmt.Errorf("auth: setup token mkdir: %w", err)
	}
	if err := os.WriteFile(setupTokenPath(workDir), []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("auth: setup token write: %w", err)
	}
	return token, nil
}

// ReadSetupToken returns the current token without generating one. Empty
// string + nil error means the file does not exist (admin already created or
// vac-api never reached EnsureSetupToken).
func ReadSetupToken(workDir string) (string, error) {
	b, err := os.ReadFile(setupTokenPath(workDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ConsumeSetupToken validates `provided` against the on-disk token in constant
// time and, on a match, deletes the file. Returns ErrSetupTokenMissing if no
// token has been generated (e.g. an attacker hitting /setup/admin on an
// already-bootstrapped install where someone manually removed the user row).
func ConsumeSetupToken(workDir, provided string) error {
	want, err := ReadSetupToken(workDir)
	if err != nil {
		return fmt.Errorf("auth: read setup token: %w", err)
	}
	if want == "" {
		return ErrSetupTokenMissing
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(provided)) != 1 {
		return ErrSetupTokenMismatch
	}
	if err := os.Remove(setupTokenPath(workDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: delete setup token: %w", err)
	}
	return nil
}

// ClearSetupToken removes the token file. Safe to call when it does not exist.
func ClearSetupToken(workDir string) error {
	if err := os.Remove(setupTokenPath(workDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func setupTokenPath(workDir string) string {
	return filepath.Join(workDir, setupTokenFile)
}
