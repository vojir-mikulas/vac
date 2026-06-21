package middleware

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// rateEntry pairs a token bucket with the last time we touched it, so the
// eviction goroutine can drop buckets for IPs we have not seen in a while.
type rateEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter is a per-IP token-bucket gate. It is safe for concurrent use.
// Construct via NewRateLimiter; do not zero-value.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateEntry
	limit   rate.Limit
	burst   int
	ttl     time.Duration
}

// NewRateLimiter returns a limiter where each IP gets `attempts` tokens that
// refill linearly over `window`. The eviction goroutine runs until ctx is
// canceled, removing buckets unused for ~1h. Pass context.Background() if
// you don't care about leak-on-shutdown (e.g. short-lived tests).
func NewRateLimiter(ctx context.Context, attempts int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: map[string]*rateEntry{},
		limit:   rate.Limit(float64(attempts) / window.Seconds()),
		burst:   attempts,
		ttl:     time.Hour,
	}
	go rl.evict(ctx)
	return rl
}

func (rl *RateLimiter) evict(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			rl.mu.Lock()
			for k, e := range rl.entries {
				if now.Sub(e.lastSeen) > rl.ttl {
					delete(rl.entries, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) getLimiter(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e, ok := rl.entries[key]
	if !ok {
		e = &rateEntry{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.entries[key] = e
	}
	e.lastSeen = time.Now()
	return e.limiter
}

// Middleware returns an http.Handler middleware that enforces the per-IP
// budget. Over-budget requests get 429 with a Retry-After header set to the
// time until the next token refill.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := rl.getLimiter(ratelimitIP(r))
		if !lim.Allow() {
			retry := rl.retryAfterSeconds()
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			handler.WriteError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// retryAfterSeconds is the time to wait for one more token to drip in.
// Rounded up to the nearest second; never zero, since "retry immediately"
// would only invite a hot loop.
func (rl *RateLimiter) retryAfterSeconds() int {
	if rl.limit <= 0 {
		return 1
	}
	secs := int(1.0/float64(rl.limit) + 0.5)
	if secs < 1 {
		return 1
	}
	return secs
}

// ratelimitIP resolves the bucket key to the originating client IP via the
// shared proxy-aware helper. Behind the bundled vac-proxy, RemoteAddr is the
// proxy's container IP for every request, so keying on it would collapse all
// clients into one bucket and defeat per-source throttling — ClientIPString
// reads the proxy-set X-Forwarded-For (when trusted) instead.
func ratelimitIP(r *http.Request) string {
	return handler.ClientIPString(r)
}
