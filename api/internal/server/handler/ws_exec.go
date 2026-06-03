package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// ShellExecutor opens an interactive PTY shell inside a running container.
// *dockercli.Compose satisfies it. Kept as a seam so the route can be wired
// (and the privilege boundary reasoned about) independently of the CLI wrapper.
type ShellExecutor interface {
	ExecInteractive(ctx context.Context, containerID string, cmd []string) (*dockercli.PtySession, error)
}

// shellPTY is the subset of *dockercli.PtySession the pump drives. Close is
// handled by the caller's defer, not here.
type shellPTY interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(rows, cols uint16) error
}

// shellPingInterval keeps an idle interactive shell's socket alive between
// keystrokes. Mirrors the ws package's own ping cadence.
const shellPingInterval = 30 * time.Second

// shellAuditAction is the audit_log action for a shell session. A WS GET isn't
// captured by the audit middleware (it only wraps mutating verbs), so the
// handler records this row itself — see recordShellAudit.
const shellAuditAction = "GET /apps/{id}/services/{name}/exec"

// resizeMsg is the only JSON control frame the client sends; everything else on
// the socket is raw terminal bytes. xterm's fit addon emits the dimensions.
type resizeMsg struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// ExecWS opens an interactive `docker exec -it {container} sh` over a WebSocket
// PTY (P3.4). This is a PRIVILEGED action — the deliberately-sandboxed control
// plane shelling into a *user app* container — so the route is feature-flagged
// off by default (VAC_ENABLE_SHELL) and every session is audit-logged. Only a
// running service with a live container id is shellable.
//
// GET (WebSocket) /api/apps/:id/services/:name/exec
func ExecWS(s *store.Store, exec ShellExecutor, opts ws.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")

		app, err := s.GetApp(r.Context(), appID)
		if err != nil {
			status, msg := http.StatusInternalServerError, "could not load app"
			if errors.Is(err, store.ErrNotFound) {
				status, msg = http.StatusNotFound, "app not found"
			}
			WriteError(w, status, msg)
			return
		}

		svc, err := s.GetService(r.Context(), appID, name)
		if err != nil {
			status, msg := http.StatusInternalServerError, "could not load service"
			if errors.Is(err, store.ErrNotFound) {
				status, msg = http.StatusNotFound, "service not found"
			}
			WriteError(w, status, msg)
			return
		}
		// A stopped/crashed container has no live id to attach to — reject before
		// the upgrade so the client gets a clean HTTP error, not a dead socket.
		if svc.Status != deploy.ServiceStatusRunning || svc.ContainerID == nil || *svc.ContainerID == "" {
			WriteError(w, http.StatusConflict, "service is not running")
			return
		}
		containerID := *svc.ContainerID

		conn, err := ws.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer conn.Close("bye")

		// Record the session like an env reveal: it crosses a trust boundary, so
		// it must leave an audit trail even though the middleware skips GETs.
		recordShellAudit(r, s, app.ID, name, containerID)

		session, err := exec.ExecInteractive(r.Context(), containerID, nil)
		if err != nil {
			_ = conn.WriteText(r.Context(), []byte("failed to open shell: "+err.Error()))
			return
		}
		// Close reaps the `docker exec` child so a dropped socket leaves no orphan.
		defer func() { _ = session.Close() }()

		pumpShell(r.Context(), conn, session)
	}
}

// pumpShell wires the PTY to the socket bidirectionally until either side ends:
// terminal output → binary frames, inbound binary → stdin, inbound text → a
// resize control. A ping ticker keeps an idle shell alive.
func pumpShell(ctx context.Context, conn *ws.Conn, session shellPTY) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// PTY → WS. Raw bytes, never JSON-wrapped, so xterm renders ANSI directly.
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if werr := conn.WriteBinary(ctx, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return // shell exited or pty closed
			}
		}
	}()

	// WS reads run in their own goroutine so the ping select can still fire while
	// blocked on a read.
	type inbound struct {
		isText bool
		data   []byte
		err    error
	}
	reads := make(chan inbound, 1)
	go func() {
		for {
			isText, data, err := conn.Read(ctx)
			// Guard the send: if the pty→ws goroutine already cancelled ctx and
			// the main loop returned, an unguarded send would block this goroutine
			// forever (a per-session leak).
			select {
			case reads <- inbound{isText, data, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(shellPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if err := conn.Ping(ctx); err != nil {
				return
			}
		case in := <-reads:
			if in.err != nil {
				return
			}
			if in.isText {
				var m resizeMsg
				if json.Unmarshal(in.data, &m) == nil && m.Type == "resize" {
					_ = session.Resize(m.Rows, m.Cols)
				}
				continue
			}
			if _, err := session.Write(in.data); err != nil {
				return
			}
		}
	}
}

// recordShellAudit writes one audit_log row for the session open, on a detached
// context (the request context is the long-lived WS; the write should settle
// independently). Mirrors the actor resolution the audit middleware uses.
func recordShellAudit(r *http.Request, s *store.Store, appID, service, containerID string) {
	summary := "opened shell into service " + service
	targetType := "app"
	entry := store.AuditEntry{
		ActorType:  store.ActorUser,
		Action:     shellAuditAction,
		TargetType: &targetType,
		TargetID:   &appID,
		Summary:    &summary,
		StatusCode: http.StatusOK,
	}
	if tok := auth.APIToken(r.Context()); tok != nil {
		uid := tok.UserID
		entry.ActorType = store.ActorAPIToken
		entry.ActorUserID = &uid
	} else if u := auth.User(r.Context()); u != nil {
		uid := u.ID
		entry.ActorUserID = &uid
	}
	if ip := webhookClientIP(r); ip != "" {
		entry.IP = &ip
	}
	if ua := r.UserAgent(); ua != "" {
		entry.UserAgent = &ua
	}
	if meta, err := json.Marshal(map[string]any{"service": service, "container_id": containerID}); err == nil {
		entry.Metadata = meta
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.InsertAuditLog(ctx, entry); err != nil {
			slog.Warn("audit: shell session insert failed", "app", appID, "err", err)
		}
	}()
}
