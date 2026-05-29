package handler

import (
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// CaddyAsk backs Caddy's on-demand-TLS `ask` hook. Caddy calls it before
// issuing a certificate for a hostname; we answer 200 only for hostnames VAC
// knows, so an attacker pointing arbitrary DNS at the box can't trigger
// unbounded ACME issuance.
//
// This endpoint is unauthenticated (Caddy can't present a session) and lives
// outside the /api auth group. It is reachable only on the internal compose
// network. An optional shared-secret header adds defence in depth.
func CaddyAsk(s *store.Store, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("X-Caddy-Ask-Token") != token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("domain")), "."))
		if host == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := s.GetDomainByHostname(r.Context(), host); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
