package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/config"
)

// TestGoroutineCountStaysBounded is the plan's proxy for the "<200 MB RSS"
// criterion: if we leak goroutines on every New / Shutdown round-trip, RSS
// growth is just downstream of that. We allow a small slack for the runtime,
// gc, and test scheduler; anything beyond a handful per cycle is a leak.
func TestGoroutineCountStaysBounded(t *testing.T) {
	// Settle the runtime before sampling — a fresh test process has some
	// scheduler goroutines that come and go in the first few ms.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		srv := New(ctx, config.Default(), nil, nil, nil)
		// We don't ListenAndServe — we exercise just the construction path
		// (rate limit eviction goroutine), since that's the one with a
		// lifetime tied to ctx.
		_ = srv
		cancel()
	}

	// Give the eviction goroutines a moment to notice ctx cancellation.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	if delta := after - before; delta > 3 {
		t.Errorf("goroutine count grew by %d after 5 New/cancel cycles (before=%d after=%d) — possible leak",
			delta, before, after)
	}
}

// TestGracefulShutdownDrainsInflight verifies that http.Server.Shutdown
// waits for in-flight requests to complete before returning. The plan calls
// this out specifically — it's easy to break by accident later.
func TestGracefulShutdownDrainsInflight(t *testing.T) {
	mux := http.NewServeMux()
	started := make(chan struct{})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()

	url := fmt.Sprintf("http://%s/slow", ln.Addr().String())

	var (
		wg   sync.WaitGroup
		body []byte
		code int
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := http.Get(url) //nolint:noctx // test client, short-lived
		if err != nil {
			t.Errorf("GET: %v", err)
			return
		}
		defer resp.Body.Close()
		code = resp.StatusCode
		body, _ = io.ReadAll(resp.Body)
	}()

	<-started
	// Shutdown begins while the slow handler is mid-flight. Should block
	// until the handler returns, not cut the response off.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	wg.Wait()
	if code != http.StatusOK {
		t.Errorf("inflight request status = %d, want 200", code)
	}
	if string(body) != "done" {
		t.Errorf("inflight body = %q, want 'done'", string(body))
	}

	if err := <-serveDone; err != nil && err != http.ErrServerClosed {
		t.Errorf("serve returned %v", err)
	}
}
