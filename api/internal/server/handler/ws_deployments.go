package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// activeDeploymentDTO is one row in the instance-wide deploy-queue snapshot: the
// deployment plus its app's display name and slug so the panel can label it
// without a second fetch.
type activeDeploymentDTO struct {
	deploymentDTO
	AppName string `json:"app_name"`
	AppSlug string `json:"app_slug"`
}

func toActiveDeploymentDTOs(rows []store.ActiveDeployment) []activeDeploymentDTO {
	out := make([]activeDeploymentDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, activeDeploymentDTO{
			deploymentDTO: toDeploymentDTO(r.Deployment),
			AppName:       r.AppName,
			AppSlug:       r.AppSlug,
		})
	}
	return out
}

// ListActiveDeployments returns the current deploy queue (running + queued
// across all apps, FIFO order). The WS stream below pushes the same shape live;
// this REST endpoint is the initial snapshot / no-WS fallback.
//
// GET /api/deployments/active
func ListActiveDeployments(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListActiveDeployments(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list active deployments")
			return
		}
		WriteJSON(w, http.StatusOK, toActiveDeploymentDTOs(rows))
	}
}

// DeploymentsWS streams the deploy queue live: it sends a snapshot on connect,
// then a fresh snapshot whenever any deployment is created, transitions, or
// settles (producers publish a payload-less change frame to the deployments
// topic). The client replaces its queue view with each snapshot.
//
// GET (WebSocket) /api/deployments/stream
func DeploymentsWS(s *store.Store, hub *ws.Hub, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Subscribe before the snapshot so a change during setup isn't missed.
		ch, cancel := hub.Subscribe(ws.DeploymentsTopic)
		defer cancel()

		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")

		if !writeDeploymentsSnapshot(r.Context(), conn, s) {
			return
		}

		// Each change frame is just a signal — re-read and push a fresh snapshot
		// in place of forwarding the (payload-less) frame. PumpFiltered drives the
		// ping keepalive and disconnect detection for us.
		conn.PumpFiltered(r.Context(), ch, func([]byte) (skip, stop bool) {
			ok := writeDeploymentsSnapshot(r.Context(), conn, s)
			return true, !ok // drop the raw frame; stop if the write failed
		})
	}
}

// writeDeploymentsSnapshot reads the active list and writes one snapshot frame.
// Returns whether the connection is still healthy.
func writeDeploymentsSnapshot(ctx context.Context, conn *ws.Conn, s *store.Store) bool {
	rows, err := s.ListActiveDeployments(ctx)
	if err != nil {
		return true // transient read error — keep the stream open, try next change
	}
	frame, err := ws.Marshal(ws.TypeDeployments, "", time.Now(), toActiveDeploymentDTOs(rows))
	if err != nil {
		return true
	}
	return conn.WriteText(ctx, frame) == nil
}
