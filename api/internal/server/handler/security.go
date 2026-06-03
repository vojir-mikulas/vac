package handler

import (
	"context"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/security"
)

// SecurityPosture provides the read-only posture checklist. *security.Posture
// satisfies it.
type SecurityPosture interface {
	Check(ctx context.Context) []security.PostureFinding
}

// SecurityTraffic provides the live traffic snapshot. *security.Monitor
// satisfies it. nil when the monitor is disabled (VAC_SECURITY_MONITOR off).
type SecurityTraffic interface {
	Snapshot(topN int) security.Snapshot
}

// SecurityHost provides read-only fail2ban / firewall state. *security.Host
// satisfies it.
type SecurityHost interface {
	Fail2ban(ctx context.Context) security.Fail2banState
	Firewall(ctx context.Context) security.FirewallState
}

// SecurityPostureHandler serves GET /api/security/posture.
func SecurityPostureHandler(p SecurityPosture) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		findings := p.Check(r.Context())
		if findings == nil {
			findings = []security.PostureFinding{}
		}
		WriteJSON(w, http.StatusOK, findings)
	}
}

// SecurityTrafficHandler serves GET /api/security/traffic. Returns an empty
// snapshot when the monitor is disabled, so the UI renders a quiet panel rather
// than erroring.
func SecurityTrafficHandler(t SecurityTraffic) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if t == nil {
			WriteJSON(w, http.StatusOK, security.Snapshot{
				TopTalkers:      []security.TopTalker{},
				RecentRequests:  []security.RecentRequest{},
				RecentAnomalies: []security.Anomaly{},
			})
			return
		}
		WriteJSON(w, http.StatusOK, t.Snapshot(20))
	}
}

// SecurityFail2banHandler serves GET /api/security/fail2ban.
func SecurityFail2banHandler(h SecurityHost) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, h.Fail2ban(r.Context()))
	}
}

// SecurityFirewallHandler serves GET /api/security/firewall.
func SecurityFirewallHandler(h SecurityHost) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, h.Firewall(r.Context()))
	}
}
