package handler

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"sync"
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
	// /health is unauthenticated (load balancers can't present a session), so a
	// naive implementation lets anyone fork `docker version` per request — a
	// resource-amplification lever on a <200 MB box. Cache the docker probe so
	// the fork happens at most once per dockerProbeTTL regardless of hit rate.
	docker := newCachedDockerProbe(dockerProbeTTL)
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

		switch docker.status(r.Context()) {
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

// dockerProbeTTL bounds how often /health may fork `docker version`. Health
// checks fire far more often than docker's state actually changes, so a short
// cache caps the subprocess rate without making the signal meaningfully stale.
const dockerProbeTTL = 10 * time.Second

// cachedDockerProbe memoizes the docker status for ttl. The mutex is held across
// a cold probe so a burst of concurrent /health hits collapses to a single fork
// (stampede protection) rather than one fork each. The probe func is injectable
// for tests.
type cachedDockerProbe struct {
	ttl   time.Duration
	probe func(context.Context) dockerStatus
	mu    sync.Mutex
	at    time.Time
	val   dockerStatus
}

func newCachedDockerProbe(ttl time.Duration) *cachedDockerProbe {
	return &cachedDockerProbe{ttl: ttl, probe: probeDocker}
}

func (c *cachedDockerProbe) status(ctx context.Context) dockerStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < c.ttl {
		return c.val
	}
	c.val = c.probe(ctx)
	c.at = time.Now()
	return c.val
}

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
