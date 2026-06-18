package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/security"
	"github.com/vojir-mikulas/vac/api/internal/store"
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

// SecurityHost provides read-only fail2ban / firewall state plus the manual
// ban override. *security.Host satisfies it.
type SecurityHost interface {
	Fail2ban(ctx context.Context) security.Fail2banState
	Firewall(ctx context.Context) security.FirewallState
	Ban(ctx context.Context, jail, ip string) (queued bool, err error)
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
		yourIP := ""
		if addr := clientIP(r); addr != nil {
			// Unmap so an IPv4-mapped IPv6 form ("::ffff:1.2.3.4") matches the
			// plain IPv4 the access log records for the same client.
			yourIP = addr.Unmap().String()
		}
		if t == nil {
			WriteJSON(w, http.StatusOK, security.Snapshot{
				TopTalkers:      []security.TopTalker{},
				RecentRequests:  []security.RecentRequest{},
				RecentAnomalies: []security.Anomaly{},
				YourIP:          yourIP,
			})
			return
		}
		snap := t.Snapshot(20)
		snap.YourIP = yourIP
		WriteJSON(w, http.StatusOK, snap)
	}
}

// SecurityAttemptLister reads the diverted unauthenticated attempts (failed
// logins, probes). *store.Store satisfies it. Unlike posture/fail2ban this needs
// no host agent — the data is the control plane's own request stream — so the
// route is always wired.
type SecurityAttemptLister interface {
	ListSecurityEvents(ctx context.Context, limit int) ([]store.SecurityEvent, error)
}

// securityAttemptDTO is the read shape for one unauthenticated attempt. Nil
// ip/user_agent collapse to "" so the UI renders a stable table.
type securityAttemptDTO struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	At        time.Time `json:"at"`
}

// SecurityAttemptsHandler serves GET /api/security/attempts — the recent
// unauthenticated attempts against the control plane, newest first. Optional
// ?limit=N (clamped by the store).
func SecurityAttemptsHandler(l SecurityAttemptLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 200
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				limit = n
			}
		}
		rows, err := l.ListSecurityEvents(r.Context(), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list attempts")
			return
		}
		out := make([]securityAttemptDTO, 0, len(rows))
		for _, e := range rows {
			out = append(out, securityAttemptDTO{
				ID:        e.ID,
				Method:    e.Method,
				Path:      e.Path,
				Status:    e.StatusCode,
				IP:        derefStr(e.IP),
				UserAgent: derefStr(e.UserAgent),
				At:        e.CreatedAt,
			})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// banRequest is the POST body for a manual ban: which jail to ban in and the IP.
type banRequest struct {
	Jail string `json:"jail"`
	IP   string `json:"ip"`
}

// SecurityBanHandler serves POST /api/security/fail2ban/ban — the operator's
// manual fail2ban override. Gated by fresh 2FA (step-up) at the route. In
// host-agent mode the ban is enqueued for the agent to apply (queued=true);
// running directly on the host it execs immediately (queued=false).
func SecurityBanHandler(h SecurityHost) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req banRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		req.Jail = strings.TrimSpace(req.Jail)
		req.IP = strings.TrimSpace(req.IP)
		queued, err := h.Ban(r.Context(), req.Jail, req.IP)
		if err != nil {
			if errors.Is(err, security.ErrInvalidBan) {
				WriteError(w, http.StatusBadRequest, "invalid jail or IP address")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not ban IP")
			return
		}
		audit.SetTarget(r.Context(), "ip", req.IP)
		audit.Describe(r.Context(), fmt.Sprintf("banned %s in jail %s", req.IP, req.Jail))
		audit.SetMetadata(r.Context(), map[string]any{"jail": req.Jail, "ip": req.IP, "queued": queued})
		WriteJSON(w, http.StatusOK, map[string]bool{"queued": queued})
	}
}
