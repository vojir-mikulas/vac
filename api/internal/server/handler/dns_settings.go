package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/dnsprovider"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// dnsSettingsDTO is the instance DNS-provider configuration as the UI sees it.
// The token is never returned — TokenSet only reports whether one is stored.
type dnsSettingsDTO struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Zone     string `json:"zone"`
	TokenSet bool   `json:"token_set"`
}

// GetDNSSettings returns the instance DNS-provider settings. enabled reflects
// VAC_DNS_AUTOMATION so the UI can hide the section when the feature is off.
func GetDNSSettings(s *store.Store, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, err := s.GetDNSSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load DNS settings")
			return
		}
		WriteJSON(w, http.StatusOK, dnsSettingsDTO{
			Enabled:  enabled,
			Provider: row.Provider,
			Zone:     row.Zone,
			TokenSet: len(row.TokenEnc) > 0,
		})
	}
}

type putDNSSettingsRequest struct {
	Provider string `json:"provider"`
	Zone     string `json:"zone"`
	// Token is the API token. An empty token on an otherwise-configured update
	// keeps the stored one (so editing the zone doesn't require re-entering it);
	// clearing the provider clears the token.
	Token string `json:"token"`
}

// PutDNSSettings stores the instance DNS-provider settings, sealing the token.
// Mounted behind RequireStepUp — it grants the box write access to the
// operator's DNS zone. 404s when the feature flag is off.
func PutDNSSettings(s *store.Store, box *crypto.Box, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			WriteError(w, http.StatusNotFound, "DNS automation is disabled (VAC_DNS_AUTOMATION)")
			return
		}
		var req putDNSSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		provider := strings.TrimSpace(req.Provider)
		zone := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Zone), "."))

		// Clearing the provider wipes the whole configuration.
		if provider == "" {
			if err := s.SetDNSSettings(r.Context(), "", nil, ""); err != nil {
				WriteError(w, http.StatusInternalServerError, "could not save DNS settings")
				return
			}
			audit.Action(r.Context(), "dns.automation_disabled", nil)
			WriteJSON(w, http.StatusOK, dnsSettingsDTO{Enabled: enabled})
			return
		}
		if provider != dnsprovider.ProviderCloudflare {
			WriteError(w, http.StatusBadRequest, "unsupported provider (only 'cloudflare' is supported)")
			return
		}
		if zone == "" {
			WriteError(w, http.StatusBadRequest, "zone is required")
			return
		}

		// Resolve the token to store: a freshly-entered one is sealed; an empty
		// one keeps the existing ciphertext (and is required on first configure).
		existing, err := s.GetDNSSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load DNS settings")
			return
		}
		tokenEnc := existing.TokenEnc
		if t := strings.TrimSpace(req.Token); t != "" {
			if box == nil {
				WriteError(w, http.StatusServiceUnavailable, "encryption is not configured (VAC_MASTER_KEY); cannot store a DNS token")
				return
			}
			sealed, err := box.Seal([]byte(t))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "could not seal DNS token")
				return
			}
			tokenEnc = sealed
		}
		if len(tokenEnc) == 0 {
			WriteError(w, http.StatusBadRequest, "an API token is required")
			return
		}

		if err := s.SetDNSSettings(r.Context(), provider, tokenEnc, zone); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save DNS settings")
			return
		}
		audit.Action(r.Context(), "dns.automation_configured", map[string]any{"provider": provider, "zone": zone})
		WriteJSON(w, http.StatusOK, dnsSettingsDTO{
			Enabled:  enabled,
			Provider: provider,
			Zone:     zone,
			TokenSet: true,
		})
	}
}

// dnsRecordOutcome is the per-domain DNS-automation result attached to an
// add-domain response so the UI can toast success/failure.
type dnsRecordOutcome struct {
	Attempted bool   `json:"attempted"`
	Created   bool   `json:"created"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DNSAutomator creates/removes DNS records for a custom domain. *dnsprovider
// .Automator satisfies it; nil disables the hook (manual records only).
type DNSAutomator interface {
	Enabled() bool
	EnsureDomainRecord(ctx context.Context, hostname string) (dnsprovider.Outcome, error)
	DeleteDomainRecord(ctx context.Context, hostname string) (dnsprovider.Outcome, error)
}

// ensureDNSRecord runs the best-effort DNS record creation and maps the result
// to the DTO outcome. A failure never fails domain creation — the manual-record
// fallback (DomainConfigPanel shows the exact record) always remains.
func ensureDNSRecord(r *http.Request, automator DNSAutomator, hostname string) *dnsRecordOutcome {
	if automator == nil || !automator.Enabled() {
		return nil
	}
	out, err := automator.EnsureDomainRecord(r.Context(), hostname)
	rec := &dnsRecordOutcome{Attempted: out.Attempted, Created: out.Created, Detail: out.Detail}
	if err != nil {
		rec.Attempted = true
		if errors.Is(err, dnsprovider.ErrPrivateAddress) {
			rec.Error = "refused private address: " + err.Error()
		} else {
			rec.Error = err.Error()
		}
		audit.Action(r.Context(), "dns.record_failed", map[string]any{"hostname": hostname, "error": rec.Error})
	} else if rec.Created {
		audit.Action(r.Context(), "dns.record_auto_created", map[string]any{"hostname": hostname})
	}
	if !rec.Attempted {
		return nil
	}
	return rec
}
