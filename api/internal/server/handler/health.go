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
}

// Health distinguishes "binary up" from "DB up" from "docker reachable" by
// pinging each. Returns 503 when either dependency is down so load balancers
// can take this instance out of rotation while the process is alive but
// deployments would fail anyway.
func Health(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthResponse{Status: "ok", Database: "skipped", Docker: "skipped"}
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
