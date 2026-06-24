// Package guard issues and verifies the signed tokens behind VAC's login gate
// ("VAC guard"): per-host cookies that prove a visitor is a logged-in VAC user,
// and short-lived exchange tokens that carry that proof across a domain boundary
// during the login redirect dance.
//
// Tokens are HMAC-SHA256 signed with a subkey derived from the master key, so
// they are unforgeable without it and stateless to verify — no DB round-trip on
// the request hot path (the forward_auth check runs on every guarded request).
// A token is bound to a single host and kind, so a cookie minted for one app
// can't be replayed against another, nor can a cookie be passed off as an
// exchange token.
package guard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Kind distinguishes the two token uses; it is part of the signed payload so the
// two can never be confused or cross-replayed.
type Kind string

const (
	// KindSession is the long-lived per-host cookie token (vac_guard) that grants
	// access to one guarded host once issued.
	KindSession Kind = "s"
	// KindExchange is the short-lived single-hop token carried in the callback URL
	// from the control-plane portal back to the guarded host, where it is traded
	// for a session cookie.
	KindExchange Kind = "x"
)

// CookieTTL is how long a minted guard cookie stays valid before the visitor is
// bounced back through the login gate. Independent of the dashboard session — a
// guard cookie only grants access to the one app, never the control plane.
const CookieTTL = 12 * time.Hour

// ExchangeTTL bounds the window between the portal minting an exchange token and
// the guarded host redeeming it. Short, since it only has to survive one browser
// redirect (cf. an OAuth authorization code).
const ExchangeTTL = 60 * time.Second

// Signer mints and verifies guard tokens. A nil *Signer means the guard is
// disabled (no master key): its methods are nil-safe, Mint returns "" and Verify
// always fails, so every caller fails closed.
type Signer struct {
	key []byte
}

// New derives a guard signing key from the master key, or returns nil when the
// master key is absent (guard disabled). Callers must treat a nil result as
// "guard unavailable" and refuse to expose a guarded service unprotected.
func New(masterKey []byte) *Signer {
	if len(masterKey) == 0 {
		return nil
	}
	// Domain-separate from crypto.Box (which keys AES-GCM on the raw master key)
	// so this HMAC key is independent of the secret-sealing key.
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("vac-guard-token-v1"))
	return &Signer{key: mac.Sum(nil)}
}

type payload struct {
	K Kind   `json:"k"` // kind
	H string `json:"h"` // host the token is bound to (lowercased)
	U string `json:"u"` // username
	E int64  `json:"e"` // expiry, unix seconds
}

// Mint returns a signed token of the given kind, bound to host and user, valid
// for ttl. Returns "" when the signer is nil.
func (s *Signer) Mint(kind Kind, host, user string, ttl time.Duration) string {
	if s == nil {
		return ""
	}
	p := payload{K: kind, H: strings.ToLower(host), U: user, E: time.Now().Add(ttl).Unix()}
	body, _ := json.Marshal(p) // a struct of strings/int never fails to marshal
	b := base64.RawURLEncoding.EncodeToString(body)
	return b + "." + base64.RawURLEncoding.EncodeToString(s.sign(b))
}

// Verify checks a token's signature, kind, host binding, and expiry, returning
// the username it carries. ok is false on any failure — bad signature, wrong
// kind, host mismatch, expired, malformed, or a nil signer.
func (s *Signer) Verify(kind Kind, host, token string) (user string, ok bool) {
	if s == nil {
		return "", false
	}
	b, sig, found := strings.Cut(token, ".")
	if !found {
		return "", false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.sign(b)) {
		return "", false
	}
	body, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return "", false
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return "", false
	}
	if p.K != kind || !strings.EqualFold(p.H, host) {
		return "", false
	}
	if time.Now().Unix() >= p.E {
		return "", false
	}
	return p.U, true
}

func (s *Signer) sign(b string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(b))
	return mac.Sum(nil)
}
