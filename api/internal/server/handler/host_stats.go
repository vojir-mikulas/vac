package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/stats"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// HostStatsProvider yields a host snapshot. *stats.Manager satisfies it.
type HostStatsProvider interface {
	Snapshot(ctx context.Context) stats.HostSnapshot
}

// HostStats serves host vitals. A normal request returns a one-off JSON
// snapshot; a WebSocket upgrade subscribes to the live `host` topic (which
// starts the host ticker on first subscriber and stops it on last).
//
// GET /api/host/stats            → JSON snapshot
// GET (WebSocket) /api/host/stats → live host stream
func HostStats(provider HostStatsProvider, hub *ws.Hub, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isWebSocketUpgrade(r) {
			WriteJSON(w, http.StatusOK, provider.Snapshot(r.Context()))
			return
		}
		ch, cancel := hub.Subscribe(ws.HostTopic)
		defer cancel()
		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")
		conn.Pump(r.Context(), ch)
	}
}

// isWebSocketUpgrade reports whether the request is a WS handshake.
func isWebSocketUpgrade(r *http.Request) bool {
	for _, v := range r.Header["Upgrade"] {
		if strings.EqualFold(v, "websocket") {
			return true
		}
	}
	return false
}
