package security

import (
	"context"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Severity classifies a posture finding. Mirrors compose.Finding's shape (E1)
// for UI consistency without importing across the package boundary.
type Severity int

const (
	SeverityOK Severity = iota
	SeverityWarn
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarn:
		return "warn"
	default:
		return "ok"
	}
}

// MarshalJSON renders the severity as its lowercase string so the UI branches on
// a stable token rather than a numeric enum.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON parses the lowercase token back to a Severity (keeps the type
// round-trippable for clients/tests; unknown tokens decode to SeverityOK).
func (s *Severity) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case `"error"`:
		*s = SeverityError
	case `"warn"`:
		*s = SeverityWarn
	default:
		*s = SeverityOK
	}
	return nil
}

// PostureFinding is one row of the posture checklist: a pass (OK) or a flagged
// issue with an operator-facing explanation.
type PostureFinding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`              // stable id, e.g. "master_key_present"
	Title    string   `json:"title"`             // short label for the checklist row
	Message  string   `json:"message"`           // what + why (+ the fix when failing)
	App      string   `json:"app,omitempty"`     // app slug for app-scoped findings
	Service  string   `json:"service,omitempty"` // service name for service-scoped findings
}

// PostureConfig is the subset of VAC config the checklist reads. Decoupled from
// config.Config so the package stays import-light and easily testable.
type PostureConfig struct {
	Exposure         string // "public" | "local"
	MasterKeyPresent bool
	MetricsTokenSet  bool
	BaseDomainSet    bool
}

// PostureStore is the read surface the checklist needs over the store.
type PostureStore interface {
	ListApps(ctx context.Context) ([]store.App, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
}

// Posture computes the read-only posture checklist on each request.
type Posture struct {
	store PostureStore
	cfg   PostureConfig
}

// NewPosture wires the checklist over the store and a config snapshot.
func NewPosture(s PostureStore, cfg PostureConfig) *Posture {
	return &Posture{store: s, cfg: cfg}
}

// Check runs every rule and returns all findings (passes and failures), so the
// UI can render a full checklist. A store error degrades gracefully: the
// store-dependent rules are skipped, the config rules still report.
func (p *Posture) Check(ctx context.Context) []PostureFinding {
	var out []PostureFinding

	// --- Config / crypto posture ---
	if p.cfg.MasterKeyPresent {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "master_key_present",
			Title:   "Master key configured",
			Message: "VAC_MASTER_KEY is set; secrets (env vars, SSH keys, TOTP, webhook URLs) are encrypted at rest."})
	} else {
		out = append(out, PostureFinding{Severity: SeverityError, Code: "master_key_present",
			Title:   "Master key missing",
			Message: "VAC_MASTER_KEY is not set — encryption is disabled and app creation is blocked. Set a 32-byte hex key."})
	}

	if p.cfg.MetricsTokenSet {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "metrics_token_set",
			Title:   "Metrics endpoint token set",
			Message: "VAC_METRICS_TOKEN gates /metrics and /debug/*, which leak instance topology and runtime internals."})
	} else {
		out = append(out, PostureFinding{Severity: SeverityWarn, Code: "metrics_token_set",
			Title:   "Metrics endpoint token unset",
			Message: "VAC_METRICS_TOKEN is unset, so /metrics and /debug/* return 404 (default-closed). Set it if you scrape metrics; leave unset otherwise."})
	}

	if p.cfg.Exposure == "public" {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "exposure_mode",
			Title:   "Public exposure with HTTPS",
			Message: "Exposure is public; session cookies carry the Secure flag and the dashboard is fronted by Caddy TLS."})
	} else {
		out = append(out, PostureFinding{Severity: SeverityWarn, Code: "exposure_mode",
			Title:   "Local exposure mode",
			Message: "Exposure is local — intended for VPN / SSH-tunnel access. Cookies are not marked Secure; do not expose this box directly to the internet."})
	}

	// --- App posture (store-backed) ---
	apps, err := p.store.ListApps(ctx)
	if err != nil {
		out = append(out, PostureFinding{Severity: SeverityWarn, Code: "app_scan",
			Title:   "App posture unavailable",
			Message: "Could not read the app list to scan for host-port publishing: " + err.Error()})
		return out
	}

	hostPortApps := 0
	for _, app := range apps {
		services, err := p.store.ListServicesForApp(ctx, app.ID)
		if err != nil {
			continue
		}
		for _, svc := range services {
			// A service with a host-published port (exposed_port) bypasses Caddy
			// and the edge network — it's reachable directly on the VPS, which is
			// what VAC's DNS-alias routing exists to avoid.
			if svc.ExposedPort != nil && *svc.ExposedPort != 0 {
				hostPortApps++
				out = append(out, PostureFinding{Severity: SeverityWarn, Code: "host_port_publish",
					Title: "Service published on a host port",
					App:   app.Slug, Service: svc.ServiceName,
					Message: "This service publishes a host port, bypassing Caddy and the vac-edge isolation. Prefer letting VAC route it over the edge network unless the port must be reachable directly."})
			}
		}
	}

	if hostPortApps == 0 {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "host_port_publish",
			Title:   "No services on host ports",
			Message: "No app publishes a host port; all HTTP traffic is fronted by Caddy over the isolated vac-edge network."})
	}

	return out
}
