package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// totpPeriod is the TOTP step length in seconds (RFC 6238 default). totpSkew is
// how many steps either side of "now" are accepted to absorb clock drift.
const (
	totpPeriod = 30
	totpSkew   = 1
)

// ErrTOTPDisabled is returned when an operation requires TOTP to be set up
// but the user has no secret stored.
var ErrTOTPDisabled = errors.New("auth: totp not configured for user")

// ErrTOTPInvalid is returned when a submitted TOTP / recovery code does not
// match anything the server can verify.
var ErrTOTPInvalid = errors.New("auth: invalid totp or recovery code")

// totpIssuer is what shows up as the account label in authenticator apps. It
// is intentionally fixed — branding lives in the deployment, not the code.
const totpIssuer = "VAC"

// recoveryCodeCount is the number of one-shot codes generated at TOTP enable.
const recoveryCodeCount = 10

// TOTPManager combines TOTP secret generation, validation, and recovery code
// bookkeeping. The Box is required because secrets are stored encrypted at
// rest — a DB leak alone must not yield working secrets.
type TOTPManager struct {
	store *store.Store
	box   *crypto.Box
}

// NewTOTPManager returns a manager. box may be nil; methods that need it will
// return an error in that case so the server can still boot without a master
// key (and without offering 2FA).
func NewTOTPManager(s *store.Store, box *crypto.Box) *TOTPManager {
	return &TOTPManager{store: s, box: box}
}

// SetupResult is what /api/auth/totp/setup hands back. The caller renders the
// otpauth URI as a QR code; secret is the raw base32 alternative for manual
// entry.
type SetupResult struct {
	Secret      string
	OtpauthURI  string
	AccountName string
}

// Setup generates a fresh secret for username and stores it encrypted as
// pending. Any prior pending or active TOTP secret on the user is overwritten.
// Caller must have already authenticated the user.
func (m *TOTPManager) Setup(ctx context.Context, userID, username string) (SetupResult, error) {
	if m.box == nil {
		return SetupResult{}, fmt.Errorf("auth: totp setup requires VAC_MASTER_KEY")
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
	})
	if err != nil {
		return SetupResult{}, fmt.Errorf("auth: totp generate: %w", err)
	}
	sealed, err := m.box.Seal([]byte(key.Secret()))
	if err != nil {
		return SetupResult{}, fmt.Errorf("auth: totp seal: %w", err)
	}
	if err := m.store.SetUserTOTPSecret(ctx, userID, sealed); err != nil {
		return SetupResult{}, fmt.Errorf("auth: totp persist: %w", err)
	}
	return SetupResult{
		Secret:      key.Secret(),
		OtpauthURI:  key.URL(),
		AccountName: username,
	}, nil
}

// Verify checks code against the user's stored secret as the LOGIN second
// factor. A ±1 step skew is tolerated to absorb clock drift. To stop replay of a
// captured code within its ~90s validity window, it identifies which time-step
// matched and atomically records it via ConsumeTOTPStep — a step at or below the
// last accepted one is rejected, so the same code authenticates at most once.
func (m *TOTPManager) Verify(ctx context.Context, userID, code string) error {
	step, err := m.matchStep(ctx, userID, code)
	if err != nil {
		return err
	}
	// Burn the step: a code at or below the last accepted step is a replay.
	fresh, err := m.store.ConsumeTOTPStep(ctx, userID, step)
	if err != nil {
		return err
	}
	if !fresh {
		return ErrTOTPInvalid
	}
	return nil
}

// verifyEnrolment checks a code during setup/enable WITHOUT consuming the step.
// Enrolment only confirms the user holds the right secret (they are already
// authenticated by session), so it must not burn the time-step — otherwise the
// user couldn't complete a 2FA login with the same code moments later.
func (m *TOTPManager) verifyEnrolment(ctx context.Context, userID, code string) error {
	_, err := m.matchStep(ctx, userID, code)
	return err
}

// matchStep returns the time-step the submitted code belongs to within the skew
// window, or ErrTOTPInvalid if it matches none. It does not touch last_totp_step.
func (m *TOTPManager) matchStep(ctx context.Context, userID, code string) (int64, error) {
	if m.box == nil {
		return 0, fmt.Errorf("auth: totp verify requires VAC_MASTER_KEY")
	}
	sealed, err := m.store.GetUserTOTPSecret(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, ErrTOTPDisabled
		}
		return 0, err
	}
	secretBytes, err := m.box.Open(sealed)
	if err != nil {
		return 0, fmt.Errorf("auth: totp open: %w", err)
	}
	secret := string(secretBytes)

	genOpts := totp.ValidateOpts{
		Period:    totpPeriod,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
	currentStep := time.Now().Unix() / totpPeriod
	// Walk the skew window and find which step the submitted code belongs to. We
	// generate the expected code per step and constant-time compare, rather than
	// using ValidateCustom, because we need the matched step number to anti-replay.
	for delta := int64(-totpSkew); delta <= totpSkew; delta++ {
		step := currentStep + delta
		expected, gerr := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), genOpts)
		if gerr != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step, nil
		}
	}
	return 0, ErrTOTPInvalid
}

// Enable verifies the code one last time, flips totp_enabled to TRUE, and
// returns 10 fresh recovery codes (stored as SHA-256 hashes; the plaintext is
// only ever shown to the user on this call).
func (m *TOTPManager) Enable(ctx context.Context, userID, code string) ([]string, error) {
	if err := m.verifyEnrolment(ctx, userID, code); err != nil {
		return nil, err
	}
	plain, hashes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := m.store.EnableUserTOTP(ctx, userID, hashes); err != nil {
		return nil, fmt.Errorf("auth: totp enable: %w", err)
	}
	return plain, nil
}

// Disable removes the secret and recovery codes. The caller must have already
// re-verified the user's password — the handler enforces that, not this
// method.
func (m *TOTPManager) Disable(ctx context.Context, userID string) error {
	if err := m.store.DisableUserTOTP(ctx, userID); err != nil {
		return fmt.Errorf("auth: totp disable: %w", err)
	}
	return nil
}

// ConsumeRecoveryCode is the recovery-code path through the TOTP login step:
// if the user's authenticator is unavailable, they can spend one of the codes
// minted at Enable. Each code works at most once.
func (m *TOTPManager) ConsumeRecoveryCode(ctx context.Context, userID, code string) error {
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return ErrTOTPInvalid
	}
	sum := sha256.Sum256([]byte(normalized))
	ok, err := m.store.ConsumeRecoveryCode(ctx, userID, hex.EncodeToString(sum[:]))
	if err != nil {
		return err
	}
	if !ok {
		return ErrTOTPInvalid
	}
	return nil
}

// generateRecoveryCodes returns n plaintext codes (for the user) and their
// SHA-256 hex hashes (for the DB).
func generateRecoveryCodes(n int) ([]string, []string, error) {
	plain := make([]string, 0, n)
	hashes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256([]byte(code))
		plain = append(plain, code)
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	return plain, hashes, nil
}

// newRecoveryCode returns a 10-char base32-like code grouped as XXXXX-XXXXX.
// 50 bits of entropy is plenty for an offline-attack-resistant one-shot code.
func newRecoveryCode() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 10
	out := make([]byte, 0, length+1)
	for i := 0; i < length; i++ {
		if i == length/2 {
			out = append(out, '-')
		}
		idx, err := randIndex(len(alphabet))
		if err != nil {
			return "", err
		}
		out = append(out, alphabet[idx])
	}
	return string(out), nil
}

// randIndex returns a uniform integer in [0, n) using rejection sampling, so the
// result is free of the modulo bias of `randomByte % n`.
func randIndex(n int) (int, error) {
	limit := 256 - (256 % n) // largest multiple of n that fits in a byte
	var b [1]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if int(b[0]) < limit {
			return int(b[0]) % n, nil
		}
	}
}

// normalizeRecoveryCode strips whitespace and lowercases so that user input
// matches the stored hash regardless of how the user copy-pasted it.
func normalizeRecoveryCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	// Strip non-alphanumeric except '-'.
	out := make([]byte, 0, len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '-':
			out = append(out, c)
		case c == ' ' || c == '\t':
			// skip
		default:
			return ""
		}
	}
	return string(out)
}
