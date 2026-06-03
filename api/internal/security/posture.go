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
	AccessLogEnabled bool // Caddy access log configured → traffic panel works
	// HostAgentEnabled is whether the operator opted into the host security agent.
	// It distinguishes "monitoring is off" (don't cry wolf) from "we looked and
	// found no firewall" when no host data is available.
	HostAgentEnabled bool
	// ExpectFirewall/ExpectFail2ban make the absence of a host firewall /
	// fail2ban a flagged finding (a box with no firewall is dangerous). Operators
	// who deliberately don't run one opt out (VAC_SECURITY_EXPECT_FIREWALL=false /
	// the `vac security-check` CLI), which downgrades the finding to an
	// informational "disabled by operator" row that never warns. Only meaningful
	// when host data is available (the agent is enabled and reporting).
	ExpectFirewall bool
	ExpectFail2ban bool
}

// PostureStore is the read surface the checklist needs over the store.
type PostureStore interface {
	ListApps(ctx context.Context) ([]store.App, error)
	ListServicesForApp(ctx context.Context, appID string) ([]store.Service, error)
}

// PostureHost is the read surface for host firewall/fail2ban state. *Host
// satisfies it. nil disables the host-backed checks.
type PostureHost interface {
	Fail2ban(ctx context.Context) Fail2banState
	Firewall(ctx context.Context) FirewallState
}

// Posture computes the read-only posture checklist on each request.
type Posture struct {
	store PostureStore
	host  PostureHost
	cfg   PostureConfig
}

// NewPosture wires the checklist over the store, the host reader (may be nil),
// and a config snapshot.
func NewPosture(s PostureStore, host PostureHost, cfg PostureConfig) *Posture {
	return &Posture{store: s, host: host, cfg: cfg}
}

// Check runs every rule and returns all findings (passes and failures), so the
// UI can render a full checklist. A store error degrades gracefully: the
// store-dependent rules are skipped, the config rules still report.
func (p *Posture) Check(ctx context.Context) []PostureFinding {
	var out []PostureFinding

	// --- Host hardening (firewall / fail2ban) ---
	// Surfaced first: a box exposed to the internet with no firewall is the most
	// dangerous posture, so it leads the checklist.
	out = append(out, p.firewallFinding(ctx))
	out = append(out, p.fail2banFinding(ctx))

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

	if p.cfg.BaseDomainSet {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "base_domain",
			Title:   "Base domain configured",
			Message: "A base domain is set; the dashboard is served over HTTPS and apps get automatic per-subdomain TLS via Caddy."})
	} else {
		out = append(out, PostureFinding{Severity: SeverityWarn, Code: "base_domain",
			Title:   "No base domain",
			Message: "No base domain is set, so the dashboard is reachable only over plain HTTP by IP. Set one with `vac set-domain <domain>` to enable HTTPS and per-app subdomains."})
	}

	if p.cfg.AccessLogEnabled {
		out = append(out, PostureFinding{Severity: SeverityOK, Code: "access_log",
			Title:   "Request monitoring active",
			Message: "Caddy's JSON access log is configured; the traffic panel and anomaly detector see every request."})
	} else {
		out = append(out, PostureFinding{Severity: SeverityWarn, Code: "access_log",
			Title:   "Request monitoring disabled",
			Message: "No Caddy access log is configured (VAC_CADDY_ACCESS_LOG), so the traffic panel stays empty and anomaly detection is off."})
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

// firewallFinding evaluates host firewall posture. When no host data is available
// (the agent is off — the default — and the control plane can't see host state)
// it reports a neutral "monitoring off" row rather than a false "not detected".
// With data, absence is an error when a firewall is expected (the default) — a
// single VPS with no firewall is dangerous — and an OK row when opted out.
func (p *Posture) firewallFinding(ctx context.Context) PostureFinding {
	var fw FirewallState
	if p.host != nil {
		fw = p.host.Firewall(ctx)
	}
	if fw.Source == "" { // couldn't read host firewall state at all
		return p.hostMonitorOff("firewall", "firewall")
	}
	if !p.cfg.ExpectFirewall {
		return PostureFinding{Severity: SeverityOK, Code: "firewall",
			Title:   "Firewall check disabled",
			Message: "Firewall checking is turned off (`vac security-check firewall off`). VAC won't warn about a missing host firewall."}
	}
	switch {
	case fw.Stale:
		return PostureFinding{Severity: SeverityWarn, Code: "firewall",
			Title:   "Firewall state stale",
			Message: "The host security agent hasn't reported recently, so firewall state may be out of date. Check that the vac-security-agent timer is running on the host."}
	case !fw.Detected:
		return PostureFinding{Severity: SeverityError, Code: "firewall",
			Title:   "No firewall detected",
			Message: "No ufw or nftables ruleset was found on the host. Running an internet-facing box without a firewall is dangerous — install and enable ufw (`ufw enable`) or nftables. Opt out with `vac security-check firewall off` if this is intentional."}
	case !fw.Active:
		return PostureFinding{Severity: SeverityError, Code: "firewall",
			Title:   "Firewall installed but inactive",
			Message: "A " + fw.Backend + " ruleset exists but the firewall is not active. Enable it (e.g. `ufw enable`) so the rules take effect."}
	default:
		return PostureFinding{Severity: SeverityOK, Code: "firewall",
			Title:   "Firewall active",
			Message: "Host firewall (" + fw.Backend + ") is active and filtering traffic."}
	}
}

// fail2banFinding evaluates host fail2ban posture. Like firewallFinding, it
// reports "monitoring off" when no host data is available, an OK row when opted
// out, and a warning when fail2ban is expected but absent.
func (p *Posture) fail2banFinding(ctx context.Context) PostureFinding {
	var f2b Fail2banState
	if p.host != nil {
		f2b = p.host.Fail2ban(ctx)
	}
	if f2b.Source == "" { // couldn't read host fail2ban state at all
		return p.hostMonitorOff("fail2ban", "fail2ban")
	}
	if !p.cfg.ExpectFail2ban {
		return PostureFinding{Severity: SeverityOK, Code: "fail2ban",
			Title:   "fail2ban check disabled",
			Message: "fail2ban checking is turned off (`vac security-check fail2ban off`). VAC won't warn about a missing fail2ban."}
	}
	switch {
	case f2b.Stale:
		return PostureFinding{Severity: SeverityWarn, Code: "fail2ban",
			Title:   "fail2ban state stale",
			Message: "The host security agent hasn't reported recently, so fail2ban state may be out of date. Check that the vac-security-agent timer is running on the host."}
	case !f2b.Detected:
		return PostureFinding{Severity: SeverityWarn, Code: "fail2ban",
			Title:   "fail2ban not detected",
			Message: "fail2ban isn't installed or readable on the host. It bans IPs that brute-force SSH and other services — recommended on an internet-facing box. Opt out with `vac security-check fail2ban off` if this is intentional."}
	case len(f2b.Jails) == 0:
		return PostureFinding{Severity: SeverityWarn, Code: "fail2ban",
			Title:   "fail2ban running with no jails",
			Message: "fail2ban is running but has no active jails, so nothing is being protected. Enable at least the sshd jail."}
	default:
		return PostureFinding{Severity: SeverityOK, Code: "fail2ban",
			Title:   "fail2ban active",
			Message: "fail2ban is running with active jails, banning abusive IPs."}
	}
}

// hostMonitorOff is the neutral finding shown when VAC has no host data for a
// firewall/fail2ban check. If the agent is enabled it's a warning (it should be
// reporting); if it's off it's an informational OK row inviting opt-in, so an
// operator who deliberately didn't install the agent isn't nagged.
func (p *Posture) hostMonitorOff(code, what string) PostureFinding {
	if p.cfg.HostAgentEnabled {
		return PostureFinding{Severity: SeverityWarn, Code: code,
			Title:   what + " state unavailable",
			Message: "The host security agent is enabled but hasn't reported yet. Check that the vac-security-agent timer is running on the host."}
	}
	return PostureFinding{Severity: SeverityOK, Code: code,
		Title:   what + " monitoring off",
		Message: "Host " + what + " monitoring is off. Enable the read-only host agent with `vac security-agent on` to surface " + what + " status here."}
}
