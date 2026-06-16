package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeRecorder captures inserted entries and signals each one so tests can wait
// out the post-response goroutine without sleeping.
type fakeRecorder struct {
	ch  chan store.AuditEntry
	sec chan store.SecurityEvent
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{ch: make(chan store.AuditEntry, 4), sec: make(chan store.SecurityEvent, 4)}
}

func (f *fakeRecorder) InsertAuditLog(_ context.Context, e store.AuditEntry) error {
	f.ch <- e
	return nil
}

func (f *fakeRecorder) InsertSecurityEvent(_ context.Context, e store.SecurityEvent) error {
	f.sec <- e
	return nil
}

// waitSecurity blocks for one diverted security event, failing if none arrives.
func (f *fakeRecorder) waitSecurity(t *testing.T) (store.SecurityEvent, bool) {
	t.Helper()
	select {
	case e := <-f.sec:
		return e, true
	case <-time.After(time.Second):
		return store.SecurityEvent{}, false
	}
}

// waitEntry blocks for one inserted entry, failing if none arrives. ok reports
// whether an entry was received (false on timeout).
func (f *fakeRecorder) waitEntry(t *testing.T) (store.AuditEntry, bool) {
	t.Helper()
	select {
	case e := <-f.ch:
		return e, true
	case <-time.After(time.Second):
		return store.AuditEntry{}, false
	}
}

// routedAudit mounts the Audit middleware on a chi router so RoutePattern is
// populated, with a handler that runs enrich against the request context.
func routedAudit(rec AuditRecorder, pattern string, enrich func(*http.Request)) http.Handler {
	r := chi.NewRouter()
	r.Use(Audit(context.Background(), rec))
	r.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if enrich != nil {
			enrich(req)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	return r
}

func TestAudit_SkipsSafeMethods(t *testing.T) {
	rec := newFakeRecorder()
	h := routedAudit(rec, "/apps", nil)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/apps", nil))

	if _, ok := rec.waitEntry(t); ok {
		t.Fatal("GET should not be audited")
	}
}

func TestAudit_RecordsMutationWithRoutePatternAndOutcome(t *testing.T) {
	rec := newFakeRecorder()
	h := routedAudit(rec, "/apps/{id}/deployments", func(req *http.Request) {
		audit.SetTarget(req.Context(), "app", "app-123")
		audit.Describe(req.Context(), "triggered deployment")
	})

	req := httptest.NewRequest(http.MethodPost, "/apps/app-123/deployments", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	h.ServeHTTP(httptest.NewRecorder(), req)

	e, ok := rec.waitEntry(t)
	if !ok {
		t.Fatal("expected an audit entry")
	}
	if want := "POST /apps/{id}/deployments"; e.Action != want {
		t.Errorf("action = %q, want %q", e.Action, want)
	}
	if e.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", e.StatusCode, http.StatusCreated)
	}
	if e.ActorType != store.ActorAnonymous {
		t.Errorf("actor type = %q, want %q (no auth in ctx)", e.ActorType, store.ActorAnonymous)
	}
	if e.TargetType == nil || *e.TargetType != "app" || e.TargetID == nil || *e.TargetID != "app-123" {
		t.Errorf("target = %v/%v, want app/app-123", e.TargetType, e.TargetID)
	}
	if e.Summary == nil || *e.Summary != "triggered deployment" {
		t.Errorf("summary = %v, want 'triggered deployment'", e.Summary)
	}
	if e.IP == nil || *e.IP != "203.0.113.7" {
		t.Errorf("ip = %v, want 203.0.113.7", e.IP)
	}
}

func TestAudit_AttributesAPITokenActor(t *testing.T) {
	rec := newFakeRecorder()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), &store.User{ID: "u-1"})
			ctx = auth.WithAPIToken(ctx, &store.APIToken{ID: "t-1", UserID: "u-1"})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(Audit(context.Background(), rec))
	r.Handle("/apps", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/apps", nil))

	e, ok := rec.waitEntry(t)
	if !ok {
		t.Fatal("expected an audit entry")
	}
	if e.ActorType != store.ActorAPIToken {
		t.Errorf("actor type = %q, want %q", e.ActorType, store.ActorAPIToken)
	}
	if e.ActorUserID == nil || *e.ActorUserID != "u-1" {
		t.Errorf("actor user id = %v, want u-1", e.ActorUserID)
	}
}

func TestAudit_DivertsAnonymousFailureToSecurityEvents(t *testing.T) {
	rec := newFakeRecorder()
	r := chi.NewRouter()
	r.Use(Audit(context.Background(), rec))
	// A login-style endpoint that rejects the unauthenticated attempt.
	r.Post("/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "198.51.100.9:443"
	req.Header.Set("User-Agent", "sqlmap/1.0")
	r.ServeHTTP(httptest.NewRecorder(), req)

	ev, ok := rec.waitSecurity(t)
	if !ok {
		t.Fatal("expected a diverted security event")
	}
	if ev.Method != http.MethodPost || ev.Path != "/auth/login" || ev.StatusCode != http.StatusUnauthorized {
		t.Errorf("event = %s %s %d, want POST /auth/login 401", ev.Method, ev.Path, ev.StatusCode)
	}
	if ev.IP == nil || *ev.IP != "198.51.100.9" {
		t.Errorf("ip = %v, want 198.51.100.9", ev.IP)
	}
	if ev.UserAgent == nil || *ev.UserAgent != "sqlmap/1.0" {
		t.Errorf("user agent = %v, want sqlmap/1.0", ev.UserAgent)
	}
	// It must NOT also land in the audit log.
	if e, ok := rec.waitEntry(t); ok {
		t.Fatalf("anonymous failure should not be audited, got %q", e.Action)
	}
}

func TestAudit_AnonymousProbeToBogusPathDiverts(t *testing.T) {
	rec := newFakeRecorder()
	r := chi.NewRouter()
	r.Use(Audit(context.Background(), rec))
	// No route registered for /.env — chi answers its built-in 404.
	r.Post("/apps", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/.env", nil))

	ev, ok := rec.waitSecurity(t)
	if !ok {
		t.Fatal("expected the probe to be diverted to security events")
	}
	if ev.Path != "/.env" || ev.StatusCode != http.StatusNotFound {
		t.Errorf("event = %s %d, want /.env 404", ev.Path, ev.StatusCode)
	}
}

func TestAudit_SkipHookSuppressesEntry(t *testing.T) {
	rec := newFakeRecorder()
	h := routedAudit(rec, "/noisy", func(req *http.Request) {
		audit.Skip(req.Context())
	})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/noisy", nil))

	if _, ok := rec.waitEntry(t); ok {
		t.Fatal("Skip() should suppress the audit entry")
	}
}
