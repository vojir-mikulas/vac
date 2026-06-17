// Package webhook holds the pure logic behind push-to-deploy (plan 01): turning
// an inbound Git webhook into a "deploy this ref or ignore it" decision. It
// authenticates the request against a per-app secret, extracts the pushed ref,
// and matches it against the app's deploy_triggers. The HTTP handler wires this
// to the store and the deploy worker; everything here is side-effect-free and
// unit-tested without a server.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Event kinds an inbound ref resolves to — they line up 1:1 with the trigger
// event values so a parsed kind can be compared to a rule's Event directly.
const (
	KindPush = store.TriggerEventPush // "push" — a branch ref
	KindTag  = store.TriggerEventTag  // "tag"  — a tag ref
)

var (
	// ErrNoRef means the payload carried no ref we could act on (e.g. a GitHub
	// ping). The caller should ignore the delivery, not fail it.
	ErrNoRef = errors.New("webhook: could not determine ref")
	// ErrBadSignature means authentication failed — the caller returns 401.
	ErrBadSignature = errors.New("webhook: signature verification failed")
)

// ParseRef classifies a Git ref into (kind, shortName). A full ref like
// refs/heads/main → ("push","main"); refs/tags/v1 → ("tag","v1"). A bare name
// (a generic caller that sends just "main") is treated as a push of that branch.
func ParseRef(ref string) (kind, name string) {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return KindPush, strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return KindTag, strings.TrimPrefix(ref, "refs/tags/")
	default:
		return KindPush, ref
	}
}

// MatchTriggers reports whether any rule fires for an event of kind/name. A
// rule matches when its Event equals kind and its Filter glob matches name; an
// empty filter matches any ref of that kind.
func MatchTriggers(triggers []store.DeployTrigger, kind, name string) bool {
	for _, t := range triggers {
		if t.Event != kind {
			continue
		}
		if t.Filter == "" || globMatch(t.Filter, name) {
			return true
		}
	}
	return false
}

// globMatch matches a Git-ref glob where `*` matches any run of characters
// (including `/`, so `release/*` matches `release/1/2`) and `?` matches exactly
// one. Everything else is literal.
func globMatch(pattern, s string) bool {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// refPayload is the slice of a webhook body we read. GitHub, GitLab, and a
// plain generic JSON post all carry the pushed ref in a top-level "ref" field.
type refPayload struct {
	Ref string `json:"ref"`
}

// deletePayload is the slice of a push body that tells us a branch was deleted:
// GitHub sets "deleted": true (and an all-zero "after"); GitLab sends an all-zero
// "after" with no "deleted" flag. Either signals the branch is gone — the
// preview-teardown trigger.
type deletePayload struct {
	After   string `json:"after"`
	Deleted bool   `json:"deleted"`
}

// IsBranchDelete reports whether a push payload deleted its branch — GitHub's
// explicit "deleted": true, or the all-zero post-image SHA that both GitHub and
// GitLab send for a delete. Used to reap the matching preview environment. A
// generic/body-less caller (no "after", no "deleted") is never a delete.
func IsBranchDelete(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var p deletePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return false
	}
	if p.Deleted {
		return true
	}
	return isAllZeroSHA(p.After)
}

// isAllZeroSHA reports whether s is a non-empty run of only '0' — the zero
// object id Git uses for a deleted ref's post-image (40 zeros for SHA-1, 64 for
// SHA-256).
func isAllZeroSHA(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}

// ExtractRef pulls the Git ref from the request: the JSON body's "ref" field,
// or failing that a ?ref= query param (the generic, body-less path). Returns
// ErrNoRef when neither is present.
func ExtractRef(r *http.Request, body []byte) (string, error) {
	var p refPayload
	if len(body) > 0 {
		_ = json.Unmarshal(body, &p)
	}
	if p.Ref != "" {
		return p.Ref, nil
	}
	if ref := r.URL.Query().Get("ref"); ref != "" {
		return ref, nil
	}
	return "", ErrNoRef
}

// Verify authenticates an inbound webhook against the app's secret, detecting
// the provider from request headers:
//
//   - GitHub: X-Hub-Signature-256 = "sha256=" + hex(HMAC-SHA256(secret, body))
//   - GitLab: X-Gitlab-Token = secret (verbatim)
//   - generic: X-VAC-Token header = secret (verbatim)
//
// Credentials are only accepted from headers — never the query string — so the
// secret can't leak into proxy/access logs. All comparisons are constant-time.
// Returns ErrBadSignature on mismatch or a missing credential.
func Verify(secret, body []byte, r *http.Request) error {
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		if verifyGitHub(secret, body, sig) {
			return nil
		}
		return ErrBadSignature
	}
	if tok := r.Header.Get("X-Gitlab-Token"); tok != "" {
		if constEq([]byte(tok), secret) {
			return nil
		}
		return ErrBadSignature
	}
	if tok := r.Header.Get("X-VAC-Token"); tok != "" && constEq([]byte(tok), secret) {
		return nil
	}
	return ErrBadSignature
}

func verifyGitHub(secret, body []byte, sigHeader string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sigHeader, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}

func constEq(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
