package handler

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Docker   string `json:"docker"`
	Caddy    string `json:"caddy"`
}

// CaddyPinger checks the reverse proxy admin API is reachable. Nil-safe.
type CaddyPinger interface {
	Ping(ctx context.Context) error
}

// Health distinguishes "binary up" from "DB up" from "docker reachable" by
// pinging each. Returns 503 when DB or docker is down so load balancers can
// take this instance out of rotation while deployments would fail anyway.
//
// Caddy is probed too but is NOT fatal: app containers keep running on vac-edge
// even when the edge proxy is briefly down — only ingress is affected.
func Health(s *store.Store, caddy CaddyPinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthResponse{Status: "ok", Database: "skipped", Docker: "skipped", Caddy: "skipped"}
		degraded := false

		if s != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := s.Ping(ctx); err != nil {
				resp.Database = "down"
				degraded = true
			} else {
				resp.Database = "ok"
			}
		}

		switch probeDocker(r.Context()) {
		case dockerStatusOK:
			resp.Docker = "ok"
		case dockerStatusDown:
			resp.Docker = "down"
			degraded = true
		case dockerStatusMissing:
			resp.Docker = "missing"
			degraded = true
		}

		if caddy != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if err := caddy.Ping(ctx); err != nil {
				resp.Caddy = "down" // non-fatal — does not flip degraded
			} else {
				resp.Caddy = "ok"
			}
		}

		if degraded {
			resp.Status = "degraded"
			WriteJSON(w, http.StatusServiceUnavailable, resp)
			return
		}
		WriteJSON(w, http.StatusOK, resp)
	}
}

type dockerStatus int

const (
	dockerStatusOK dockerStatus = iota
	dockerStatusDown
	dockerStatusMissing
)

func probeDocker(parent context.Context) dockerStatus {
	if _, err := exec.LookPath("docker"); err != nil {
		return dockerStatusMissing
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	cmd.Env = []string{"PATH=" + pathOrDefault()}
	if err := cmd.Run(); err != nil {
		return dockerStatusDown
	}
	return dockerStatusOK
}

func pathOrDefault() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}
