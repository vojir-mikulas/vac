package ws

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

const (
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
)

// ErrUnauthenticated is returned by Accept when no authenticated user is on the
// request context. The handler should already sit behind RequireSession; this
// is defence in depth.
var ErrUnauthenticated = errors.New("ws: unauthenticated")

// AcceptOptions controls the upgrade. OriginPatterns are extra allowed Origin
// hosts beyond the request's own host (which coder/websocket always allows).
// InsecureSkipVerify disables the Origin check entirely — only ever set from
// VAC_EXPOSURE=local, never in public mode.
type AcceptOptions struct {
	OriginPatterns     []string
	InsecureSkipVerify bool
}

// Conn is a thin wrapper over a coder/websocket connection that keeps the WS
// library contained in this package. It is server-push only — inbound client
// frames are read and discarded (to process control frames and detect close).
type Conn struct {
	ws *websocket.Conn
}

// Accept upgrades the HTTP request to a WebSocket. It rejects (401) any request
// without an authenticated user and enforces the Origin policy. On error the
// response has already been written.
func Accept(w http.ResponseWriter, r *http.Request, opts AcceptOptions) (*Conn, error) {
	if auth.User(r.Context()) == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return nil, ErrUnauthenticated
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:     opts.OriginPatterns,
		InsecureSkipVerify: opts.InsecureSkipVerify,
	})
	if err != nil {
		// websocket.Accept already wrote the failure response (e.g. 403 on a
		// forbidden Origin).
		return nil, err
	}
	return &Conn{ws: c}, nil
}

// WriteText sends one text frame with a bounded deadline.
func (c *Conn) WriteText(ctx context.Context, msg []byte) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, msg)
}

// Close tears the connection down with a normal close code.
func (c *Conn) Close(reason string) {
	_ = c.ws.Close(websocket.StatusNormalClosure, reason)
}

// Pump writes every frame from ch to the client until ch is closed (the
// subscriber was cancelled or dropped), ctx is done, or the client disconnects.
// A closed channel ends the stream with a policy-violation close (the hub drops
// slow consumers by closing their channel).
func (c *Conn) Pump(ctx context.Context, ch <-chan []byte) {
	c.PumpFiltered(ctx, ch, nil)
}

// PumpFiltered is Pump with a per-frame hook. filter returns (skip, stop): skip
// drops the frame without writing; stop ends the stream after handling it. A
// nil filter writes every frame and never stops early. Used by the log handlers
// to dedup the backlog/live overlap by id and to end the build stream on the
// terminator frame.
func (c *Conn) PumpFiltered(ctx context.Context, ch <-chan []byte, filter func(msg []byte) (skip, stop bool)) {
	// CloseRead drains inbound frames, answers control frames, and cancels the
	// returned context when the peer goes away.
	ctx = c.ws.CloseRead(ctx)

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				_ = c.ws.Close(websocket.StatusPolicyViolation, "subscriber dropped")
				return
			}
			if filter != nil {
				skip, stop := filter(msg)
				if !skip {
					if err := c.WriteText(ctx, msg); err != nil {
						return
					}
				}
				if stop {
					return
				}
				continue
			}
			if err := c.WriteText(ctx, msg); err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
