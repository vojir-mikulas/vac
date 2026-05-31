// Package admin implements out-of-band administrative subcommands invoked via
// the vac-api binary (e.g. `vac-api reset-password admin`). These exist for
// account-recovery scenarios where the operator has lost dashboard access but
// still has shell access to the host — the trust model the master key already
// assumes.
package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// minPasswordLen mirrors the setup-form requirement. Kept here as a local
// constant rather than imported from the handler package to avoid pulling the
// HTTP stack into the CLI binary.
const minPasswordLen = 12

// ResetPassword is the entry point for `vac-api reset-password <username>`.
// It prompts twice for a new password on the controlling TTY, rotates the
// stored bcrypt hash, and revokes every existing session so any stolen cookies
// stop working immediately.
func ResetPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: vac-api reset-password <username>")
	}
	username := strings.TrimSpace(args[0])
	if username == "" {
		return errors.New("username is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return errors.New("VAC_DATABASE_URL is not set — cannot connect to the database")
	}

	password, err := readNewPassword(stdin, stdout)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer pool.Close()

	s := store.New(pool)

	user, err := s.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("user %q not found", username)
		}
		return fmt.Errorf("lookup user: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := s.UpdateUserPassword(ctx, user.ID, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	revoked, err := s.RevokeAllSessionsForUser(ctx, user.ID)
	if err != nil {
		// Password is already rotated; surface the cleanup failure so the
		// operator can re-run if needed, but don't pretend the reset failed.
		fmt.Fprintf(stderr, "warning: password updated but session revocation failed: %v\n", err)
	}

	fmt.Fprintf(stdout, "Password updated for %q. Revoked %d session(s).\n", user.Username, revoked)
	if user.TOTPEnabled {
		fmt.Fprintln(stdout, "Note: two-factor authentication is still enabled on this account.")
	}
	return nil
}

// readNewPassword prompts twice and confirms the values match. Uses
// golang.org/x/term when stdin is a TTY so the input is not echoed; falls back
// to a plain read for piped input (CI / scripted recovery).
func readNewPassword(stdin io.Reader, stdout io.Writer) (string, error) {
	fd := -1
	if f, ok := stdin.(*os.File); ok {
		fd = int(f.Fd())
	}

	prompt := func(label string) (string, error) {
		fmt.Fprint(stdout, label)
		if fd >= 0 && term.IsTerminal(fd) {
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(stdout)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
		// Non-interactive: read one line.
		buf := make([]byte, 0, 256)
		one := make([]byte, 1)
		for {
			n, err := stdin.Read(one)
			if n > 0 {
				if one[0] == '\n' {
					break
				}
				buf = append(buf, one[0])
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", err
			}
		}
		return strings.TrimRight(string(buf), "\r"), nil
	}

	pw, err := prompt("New password: ")
	if err != nil {
		return "", err
	}
	if len(pw) < minPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	confirm, err := prompt("Confirm password: ")
	if err != nil {
		return "", err
	}
	if pw != confirm {
		return "", errors.New("passwords do not match")
	}
	return pw, nil
}
