package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// AuditRecorder is the slice of *store.Store the audit middleware writes
// against. An interface so server wiring and tests can substitute a fake. It
// records two streams: operator/system actions go to the audit_log; the
// unauthenticated attempts it diverts (failed logins, probes) go to
// security_events instead, so the activity feed stays an action log.
type AuditRecorder interface {
	InsertAuditLog(ctx context.Context, e store.AuditEntry) error
	InsertSecurityEvent(ctx context.Context, e store.SecurityEvent) error
}

// securityPathMax bounds the stored probe path. Scanners hit long, junk URLs;
// the path is for legibility ("they tried /api/.env"), not forensics, so a
// generous cap keeps a runaway URL from bloating a row.
const securityPathMax = 256

// auditPersistTimeout bounds the post-response write so a slow DB can't pin a
// goroutine after the client already has its answer.
const auditPersistTimeout = 5 * time.Second

// Audit write-pool sizing. A small fixed pool drains a bounded queue so a slow
// DB under a burst of mutating requests can't spawn an unbounded number of
// detached goroutines (each previously lived up to auditPersistTimeout).
const (
	auditWorkers   = 4
	auditQueueSize = 256
)

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
func Audit(ctx context.Context, rec AuditRecorder) func(http.Handler) http.Handler {
	// Fixed worker pool draining a bounded queue. Writes happen on a detached
	// context (the request ctx dies when ServeHTTP returns, but the response is
	// already sent); the pool caps both the goroutine count and the in-flight
	// backlog so a slow DB under load degrades to dropped audit rows, not an
	// unbounded goroutine pile-up. The workers stop when ctx (the server
	// lifetime) is cancelled, so they don't outlive the server.
	jobs := make(chan func(), auditQueueSize)
	var dropped atomic.Int64
	for i := 0; i < auditWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-jobs:
					job()
				}
			}
		}()
	}
	// enqueue offers a persist job to the pool, dropping (with a counted warning)
	// when the queue is full rather than blocking the response path. label is for
	// the drop log only.
	enqueue := func(label string, job func()) {
		select {
		case jobs <- job:
		default:
			n := dropped.Add(1)
			slog.Warn("audit: write queue full, dropping entry", "what", label, "dropped_total", n)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			record := audit.NewRecord()
			ctx := audit.WithRecord(r.Context(), record)
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			// persist builds and enqueues the row for the given status. Run exactly
			// once, from the defer below, so it fires whether the handler returned
			// normally or panicked.
			var persisted bool
			persist := func(status int) {
				if persisted || record.Skip {
					return
				}
				persisted = true
				if status == 0 {
					status = http.StatusOK // handler wrote a body without WriteHeader
				}
				// An unauthenticated mutation that failed is not operator activity —
				// it's a failed login or a scanner POSTing to a bogus path. Divert it
				// to security_events so it surfaces as a probe rather than burying the
				// real feed. (Anonymous 2xx, e.g. a successful login, stays in the
				// audit log: it's a legitimate, attributable-to-no-user action.)
				if actorType, _ := resolveActor(r.Context()); actorType == store.ActorAnonymous && status >= 400 {
					ev := buildSecurityEvent(r, status)
					enqueue("security:"+ev.Method+" "+ev.Path, func() { persistSecurity(rec, ev) })
					return
				}
				entry := buildEntry(r, record, status)
				enqueue(entry.Action, func() { persistAudit(rec, entry) })
			}

			// Persist in a defer so a panicking handler still produces an audit row.
			// chimw.Recoverer sits *outside* this middleware and turns the panic into
			// a 500 only after unwinding past here, so without the defer the
			// highest-severity failures — a destructive handler that crashes — would
			// leave no trace. On a panic we record the inferred 500, then re-panic so
			// Recoverer still does its job.
			defer func() {
				if rv := recover(); rv != nil {
					persist(http.StatusInternalServerError)
					panic(rv)
				}
				persist(ww.Status())
			}()

			next.ServeHTTP(ww, r.WithContext(ctx))
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

func persistAudit(rec AuditRecorder, entry store.AuditEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), auditPersistTimeout)
	defer cancel()
	if err := rec.InsertAuditLog(ctx, entry); err != nil {
		slog.Warn("audit: insert failed", "action", entry.Action, "err", err)
	}
}

// buildSecurityEvent captures the bare shape of an unauthenticated attempt: the
// method, the raw path that was hit (truncated), the outcome, and the source.
// The raw path — not chi's matched pattern — is what's useful here: it shows
// exactly what was probed ("/api/.env"), where the audit feed wants the grouped
// route template.
func buildSecurityEvent(r *http.Request, status int) store.SecurityEvent {
	ev := store.SecurityEvent{
		Method:     r.Method,
		Path:       truncate(r.URL.Path, securityPathMax),
		StatusCode: status,
	}
	if ip := clientIP(r); ip != "" {
		ev.IP = &ip
	}
	if ua := r.UserAgent(); ua != "" {
		ua = truncate(ua, securityPathMax)
		ev.UserAgent = &ua
	}
	return ev
}

func persistSecurity(rec AuditRecorder, ev store.SecurityEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), auditPersistTimeout)
	defer cancel()
	if err := rec.InsertSecurityEvent(ctx, ev); err != nil {
		slog.Warn("audit: security event insert failed", "path", ev.Path, "err", err)
	}
}

// truncate caps s to n bytes (rune-safe enough for storage — we never re-split).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
