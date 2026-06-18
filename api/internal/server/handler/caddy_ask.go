package handler

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ControlDomainChecker reports whether a hostname is the configured
// control-plane domain. The proxy manager implements it; vac-api uses it so
// Caddy can issue a cert for the dashboard host without a matching domain row
// (the dashboard isn't an app, so it never appears in the domains table).
type ControlDomainChecker interface {
	IsControlDomain(host string) bool
}

// AutoHostChecker reports whether a hostname is one of VAC's currently-derived
// automatic subdomains. Since auto hosts are no longer stored as rows (plan 09
// F1), CaddyAsk consults this so on-demand TLS issuance is still allowed for
// them. The proxy manager implements it; nil disables the auto-host allowance.
type AutoHostChecker interface {
	IsAutoHost(ctx context.Context, host string) (bool, error)
}

// CaddyAsk backs Caddy's on-demand-TLS `ask` hook. Caddy calls it before
// issuing a certificate for a hostname; we answer 200 only for hostnames VAC
// knows, so an attacker pointing arbitrary DNS at the box can't trigger
// unbounded ACME issuance.
//
// This endpoint is unauthenticated (Caddy can't present a session) and lives
// outside the /api auth group. It is reachable only on the internal compose
// network. An optional shared-secret header adds defence in depth.
func CaddyAsk(s *store.Store, token string, ctrl ControlDomainChecker, auto AutoHostChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Caddy-Ask-Token")), []byte(token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("domain")), "."))
		if host == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if ctrl != nil && ctrl.IsControlDomain(host) {
			w.WriteHeader(http.StatusOK)
			return
		}
		// A known custom domain (assigned or not — an added-but-unpointed domain
		// pre-warms its cert by design, see plan 09 Phase 1).
		if d, err := s.GetDomainByHostname(r.Context(), host); err == nil {
			// A bring-your-own-cert host must NOT ACME-issue: VAC serves the
			// uploaded cert (dns-automation plan B). Refusing here closes the brief
			// window between an upload and the cert reaching Caddy's cache.
			if d.TLSCertSource == "uploaded" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		// A currently-derived auto host has no row; allow it explicitly.
		if auto != nil {
			if ok, err := auto.IsAutoHost(r.Context(), host); err == nil && ok {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusForbidden)
	}
}
