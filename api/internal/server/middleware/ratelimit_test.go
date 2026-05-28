package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// alwaysOK is the inner handler the limiter wraps in these tests.
func alwaysOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func reqFromIP(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.RemoteAddr = ip + ":4242"
	return r
}

// TestRateLimiterBlocksSixthRequest is the plan's exit criterion: 5 per
// 15min, the 6th hits 429.
func TestRateLimiterBlocksSixthRequest(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, 15*time.Minute)
	h := rl.Middleware(alwaysOK())

	for i := 1; i <= 5; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, reqFromIP("1.2.3.4"))
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: code = %d, want 200", i, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("1.2.3.4"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request: code = %d, want 429", rr.Code)
	}
	retry := rr.Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("missing Retry-After header")
	}
	if n, err := strconv.Atoi(retry); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want positive integer", retry)
	}
}

// TestRateLimiterIsolatesIPs is the worst-thing-that-could-happen test: one
// noisy IP must not lock out a different IP.
func TestRateLimiterIsolatesIPs(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 2, time.Hour)
	h := rl.Middleware(alwaysOK())

	// Burn IP A's budget.
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, reqFromIP("10.0.0.1"))
		if rr.Code != http.StatusOK {
			t.Fatalf("A %d: %d", i, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("10.0.0.1"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("A overspend: %d, want 429", rr.Code)
	}

	// IP B still has its full budget.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("10.0.0.2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("B first: %d, want 200", rr.Code)
	}
}

// TestRateLimiterRefills exercises the token-bucket refill path on a very
// fast window so the test runs in milliseconds.
func TestRateLimiterRefills(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 1, 50*time.Millisecond)
	h := rl.Middleware(alwaysOK())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("9.9.9.9"))
	if rr.Code != http.StatusOK {
		t.Fatalf("first: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("9.9.9.9"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second (immediate): %d, want 429", rr.Code)
	}
	time.Sleep(100 * time.Millisecond)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFromIP("9.9.9.9"))
	if rr.Code != http.StatusOK {
		t.Fatalf("third (after refill): %d, want 200", rr.Code)
	}
}
