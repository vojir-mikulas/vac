package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// authed wraps h so every request carries an authenticated user, mimicking the
// RequireSession-guarded route group the WS endpoints live under.
func authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithUser(r.Context(), &store.User{ID: "u1"}))
		h(w, r)
	}
}

func TestAcceptDeliversFrames(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(authed(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close("done")
		ch, cancel := h.Subscribe("t")
		defer cancel()
		c.Pump(r.Context(), ch)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	// Give the server goroutine a moment to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	h.Publish("t", []byte("frame1"))

	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "frame1" {
		t.Fatalf("got %q want frame1", data)
	}
}

func TestAcceptRejectsUnauthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No authed wrapper — user is absent.
		if _, err := Accept(w, r, AcceptOptions{InsecureSkipVerify: true}); err != nil {
			return
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err == nil {
		t.Fatal("expected unauthenticated dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func TestAcceptRejectsForeignOrigin(t *testing.T) {
	srv := httptest.NewServer(authed(func(w http.ResponseWriter, r *http.Request) {
		// Strict: only the request's own host is allowed (no skip-verify).
		if _, err := Accept(w, r, AcceptOptions{}); err != nil {
			return
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example.com"}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected foreign-origin dial to be rejected")
	}
}
