// Package domainstatus is an always-on, in-memory projection of each managed
// host's DNS + TLS configuration status (plan 09 F3). It does outbound DNS
// resolution and TLS observation for every custom domain and derived auto host,
// classifies the result, and serves a single honest status the UI renders
// without the operator guessing.
//
// Status is deliberately NOT persisted (plan 09 F3 §1): DNS and "is a cert being
// served" are live external facts, cheap to recompute and stale by nature.
// Persisting them would buy a few seconds of cold-start warmth at the cost of
// write amplification and a second source of truth that can disagree with
// reality. The only durable state (cert-expiry de-dupe stamps) stays owned by
// the certcheck notification job.
package domainstatus

import "time"

// Status states. The first five are surfaced as domain status; `checking` is a
// transient pre-result state the UI renders as a neutral spinner.
const (
	// StateChecking — no observation yet this lifetime (cold start / just added).
	StateChecking = "checking"
	// StateAwaitingDNS — hostname does not resolve to this VPS yet.
	StateAwaitingDNS = "awaiting_dns"
	// StateMisconfigured — resolves, but to a different IP / wrong record type.
	StateMisconfigured = "misconfigured"
	// StateIssuing — points here, cert not yet observed active.
	StateIssuing = "issuing"
	// StateActive — DNS valid AND a leaf cert is served with a future NotAfter.
	StateActive = "active"
	// StateError — route push failed (set imperatively by the proxy manager).
	StateError = "error"
)

// Status is the projected state of one host.
type Status struct {
	State        string     `json:"state"`
	Detail       string     `json:"detail,omitempty"`
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	LastChecked  *time.Time `json:"last_checked,omitempty"`
}
