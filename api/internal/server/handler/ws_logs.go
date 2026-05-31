package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// backlogLimit caps how many rows we replay on connect before tailing live.
const backlogLimit = 1000

// BuildLogsWS streams a deployment's build log: it replays the persisted lines
// then tails new ones live, closing when the deployment settles. The replay and
// the live tail are deduped by row id so a line written between the two phases
// is shown exactly once.
//
// GET (WebSocket) /api/deployments/:did/logs
func BuildLogsWS(s *store.Store, hub *ws.Hub, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did := chi.URLParam(r, "did")
		if _, err := s.GetDeployment(r.Context(), did); err != nil {
			// Pre-upgrade: a plain HTTP error is still appropriate.
			status := http.StatusInternalServerError
			msg := "could not load deployment"
			if errors.Is(err, store.ErrNotFound) {
				status, msg = http.StatusNotFound, "deployment not found"
			}
			WriteError(w, status, msg)
			return
		}

		// Subscribe before reading the backlog so lines written during the
		// replay are buffered and then deduped, never dropped.
		ch, cancel := hub.Subscribe(ws.BuildTopic(did))
		defer cancel()

		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")

		maxID, ok := replayBuildLogs(r.Context(), conn, s, did)
		if !ok {
			return
		}

		// Re-check after replay: if the deploy settled while we were setting up
		// (its build-end frame may have been published before we subscribed),
		// the replayed backlog is already the whole story. Send the terminator
		// so the client stops reconnecting instead of re-replaying in a loop.
		if d, err := s.GetDeployment(r.Context(), did); err == nil && deploy.IsTerminalDeploymentStatus(d.Status) {
			if end, ferr := ws.Control(ws.TypeBuildEnd, time.Now()); ferr == nil {
				_ = conn.WriteText(r.Context(), end)
			}
			return
		}

		conn.PumpFiltered(r.Context(), ch, func(msg []byte) (skip, stop bool) {
			f, err := ws.Decode(msg)
			if err != nil {
				return true, false
			}
			if f.Type == ws.TypeBuildEnd {
				return false, true // forward the terminator, then end the stream
			}
			if f.ID != 0 && f.ID <= maxID {
				return true, false // already replayed
			}
			return false, false
		})
	}
}

// replayBuildLogs writes the persisted build log to the connection in id order,
// paging through the store. Returns the highest id written and whether the
// connection is still healthy.
func replayBuildLogs(ctx context.Context, conn *ws.Conn, s *store.Store, did string) (int64, bool) {
	var after int64
	for {
		rows, err := s.ListDeploymentLogs(ctx, did, after, backlogLimit)
		if err != nil || len(rows) == 0 {
			return after, err == nil
		}
		for _, row := range rows {
			frame, ferr := ws.LogFrame(ws.TypeBuild, ptrStr(row.ServiceName), row.ID, row.Timestamp, buildLogDTO{
				Stream:      row.Stream,
				Message:     row.Message,
				ServiceName: row.ServiceName,
			})
			if ferr != nil {
				continue
			}
			if err := conn.WriteText(ctx, frame); err != nil {
				return after, false
			}
			after = row.ID
		}
		if len(rows) < backlogLimit {
			return after, true
		}
	}
}

// RuntimeLogsWS streams an app's container logs: it replays the most recent
// persisted lines (optionally filtered to one service) then tails live, deduped
// by row id. Runtime capture is always-on, so the live topic exists whether or
// not anyone is watching — this handler is purely a reader.
//
// GET (WebSocket) /api/apps/:id/logs
// GET (WebSocket) /api/apps/:id/services/:name/logs
func RuntimeLogsWS(s *store.Store, hub *ws.Hub, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		service := chi.URLParam(r, "name") // "" for the all-services route
		if _, err := s.GetApp(r.Context(), appID); err != nil {
			status, msg := http.StatusInternalServerError, "could not load app"
			if errors.Is(err, store.ErrNotFound) {
				status, msg = http.StatusNotFound, "app not found"
			}
			WriteError(w, status, msg)
			return
		}

		ch, cancel := hub.Subscribe(ws.LogsTopic(appID))
		defer cancel()

		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")

		maxID, ok := replayRuntimeLogs(r.Context(), conn, s, appID, service)
		if !ok {
			return
		}

		conn.PumpFiltered(r.Context(), ch, func(msg []byte) (skip, stop bool) {
			f, err := ws.Decode(msg)
			if err != nil {
				return true, false
			}
			if service != "" && f.Service != service {
				return true, false // other service on the shared app topic
			}
			if f.ID != 0 && f.ID <= maxID {
				return true, false // already replayed
			}
			return false, false
		})
	}
}

// replayRuntimeLogs writes the most recent persisted lines in chronological
// order and returns the highest id written.
func replayRuntimeLogs(ctx context.Context, conn *ws.Conn, s *store.Store, appID, service string) (int64, bool) {
	rows, err := s.ListRuntimeLogs(ctx, appID, service, 0, backlogLimit)
	if err != nil {
		return 0, false
	}
	// ListRuntimeLogs returns newest-first; replay oldest-first.
	var maxID int64
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.ID > maxID {
			maxID = row.ID
		}
		frame, ferr := ws.LogFrame(ws.TypeLog, row.ServiceName, row.ID, row.Timestamp, runtimeLogDTO{
			Stream:  row.Stream,
			Message: row.Message,
		})
		if ferr != nil {
			continue
		}
		if err := conn.WriteText(ctx, frame); err != nil {
			return maxID, false
		}
	}
	return maxID, true
}

// buildLogDTO mirrors deploy.buildLogPayload for the replay path (the deploy
// package owns the live-frame shape; we keep the JSON tags identical).
type buildLogDTO struct {
	Stream      string  `json:"stream"`
	Message     string  `json:"message"`
	ServiceName *string `json:"service_name,omitempty"`
}

// runtimeLogDTO mirrors logstream.runtimeLogData for the replay path.
type runtimeLogDTO struct {
	Stream  string `json:"stream"`
	Message string `json:"message"`
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
