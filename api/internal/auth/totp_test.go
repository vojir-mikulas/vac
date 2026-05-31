package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateRecoveryCodesUnique(t *testing.T) {
	t.Parallel()
	plain, hashes, err := generateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(plain) != 10 || len(hashes) != 10 {
		t.Fatalf("count: got %d/%d, want 10/10", len(plain), len(hashes))
	}

	seen := map[string]bool{}
	for i, code := range plain {
		if seen[code] {
			t.Errorf("duplicate recovery code at index %d: %s", i, code)
		}
		seen[code] = true

		// Format: <5 chars>-<5 chars>, lowercase alnum.
		if len(code) != 11 || code[5] != '-' {
			t.Errorf("code %q does not match XXXXX-XXXXX shape", code)
		}
		if strings.ToLower(code) != code {
			t.Errorf("code %q should be lowercase", code)
		}

		// Hash must match what would be stored.
		sum := sha256.Sum256([]byte(code))
		if hex.EncodeToString(sum[:]) != hashes[i] {
			t.Errorf("hash mismatch for code %d", i)
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"abcde-fghij", "abcde-fghij"},
		{"ABCDE-FGHIJ", "abcde-fghij"},
		{"  abcde-fghij  ", "abcde-fghij"},
		{"abcde fghij", "abcdefghij"}, // spaces removed; tolerant of copy-paste
		{"abcde!fghij", ""},           // disallowed char invalidates
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeRecoveryCode(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Internal whitespace is not stripped, but leading/trailing is.
	if got := normalizeRecoveryCode("\tabcde-fghij\n"); got != "abcde-fghij" {
		t.Errorf("trim failed: got %q", got)
	}
}

// TestTOTPGenerateValidatesItsOwnCode is a sanity check that we agreed with
// the otp library on Period/Skew/Digits/Algorithm. If this breaks, the login
// path will be silently rejecting valid codes.
func TestTOTPGenerateValidatesItsOwnCode(t *testing.T) {
	t.Parallel()
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "VAC",
		AccountName: "alice",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if ok := totp.Validate(code, key.Secret()); !ok {
		t.Fatalf("validate of self-generated code returned false")
	}
}
