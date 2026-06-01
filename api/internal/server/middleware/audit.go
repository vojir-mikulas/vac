package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// AuditRecorder is the slice of *store.Store the audit middleware writes
// against. An interface so server wiring and tests can substitute a fake.
type AuditRecorder interface {
	InsertAuditLog(ctx context.Context, e store.AuditEntry) error
}

// auditPersistTimeout bounds the post-response write so a slow DB can't pin a
// goroutine after the client already has its answer.
const auditPersistTimeout = 5 * time.Second

// Audit records one row per *mutating* request (POST/PUT/PATCH/DELETE) to the
// audit_log: who (actor), what (method + matched route), and the outcome
// (status code), plus ip / user-agent. Handlers enrich the entry in passing via
// the audit package (target, summary, metadata); this middleware supplies
// everything else and persists once the handler returns.
//
// Mount it innermost in the /api stack (after Auth + CSRF) so the actor is
// resolved and the captured status reflects the handler's own response. A
// failed insert is logged, never surfaced — auditing must not break the request
// it is recording. GETs and other safe methods are skipped: reads aren't audit
// events and would bury the signal.
func Audit(rec AuditRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			record := audit.NewRecord()
			ctx := audit.WithRecord(r.Context(), record)
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r.WithContext(ctx))

			if record.Skip {
				return
			}

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // handler wrote a body without WriteHeader
			}

			entry := buildEntry(r, record, status)
			// Persist on a detached context: the request context is cancelled
			// the moment ServeHTTP returns, but the response is already sent.
			go persist(rec, entry) //nolint:gosec // G118: detached context is intentional — the audit write must outlive the request ctx (cancelled when ServeHTTP returns)
		})
	}
}

// buildEntry assembles the store row from the request, the handler-enriched
// record, and the captured status. Kept pure for testability.
func buildEntry(r *http.Request, record *audit.Record, status int) store.AuditEntry {
	actorType, actorUserID := resolveActor(r.Context())

	entry := store.AuditEntry{
		ActorType:  actorType,
		Action:     r.Method + " " + routePattern(r),
		StatusCode: status,
		// Only a successful mutation carries a usable inverse — a 4xx/5xx left
		// the prior state in place, so there's nothing to undo.
		Revertable: record.Revertable && status >= 200 && status < 300,
	}
	entry.ActorUserID = actorUserID
	if ip := clientIP(r); ip != "" {
		entry.IP = &ip
	}
	if ua := r.UserAgent(); ua != "" {
		entry.UserAgent = &ua
	}
	if record.TargetType != "" {
		entry.TargetType = &record.TargetType
	}
	if record.TargetID != "" {
		entry.TargetID = &record.TargetID
	}
	if record.Summary != "" {
		entry.Summary = &record.Summary
	}
	if len(record.Metadata) > 0 {
		if raw, err := json.Marshal(record.Metadata); err == nil {
			entry.Metadata = raw
		} else {
			slog.Warn("audit: metadata marshal failed", "err", err)
		}
	}
	return entry
}

func persist(rec AuditRecorder, entry store.AuditEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), auditPersistTimeout)
	defer cancel()
	if err := rec.InsertAuditLog(ctx, entry); err != nil {
		slog.Warn("audit: insert failed", "action", entry.Action, "err", err)
	}
}

// resolveActor reports the actor type and (where known) user id. Token auth
// wins over cookie — it is the more specific attribution.
func resolveActor(ctx context.Context) (string, *string) {
	if tok := auth.APIToken(ctx); tok != nil {
		uid := tok.UserID
		return store.ActorAPIToken, &uid
	}
	if u := auth.User(ctx); u != nil {
		uid := u.ID
		return store.ActorUser, &uid
	}
	// A mutating request with no resolved actor: an unauthenticated attempt
	// (a failed login, a missing session). Recorded, attributed to no user.
	return store.ActorAnonymous, nil
}

// routePattern returns chi's matched template ("/apps/{id}/deployments") so the
// action groups by route rather than fanning out per id. Falls back to the raw
// path if the pattern is unavailable (no route matched, e.g. a 404).
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return r.URL.Path
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// clientIP mirrors the auth handlers: the connection's remote host, stripped of
// its port. vac-proxy terminates TLS in front, but the control plane trusts the
// socket peer here rather than a spoofable forwarded header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
