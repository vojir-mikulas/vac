//go:build integration

package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/server"
)

// TestWebhookIsRateLimited covers item 5: the unauthenticated inbound webhook
// must be per-IP rate-limited so an attacker can't drive unbounded
// deploy-enqueue / HMAC-compute attempts. Unknown-app deliveries 404 within
// budget; the over-budget request gets 429 + Retry-After before reaching the
// handler at all.
func TestWebhookIsRateLimited(t *testing.T) {
	s := setupPool(t)
	cfg := config.Default()
	cfg.WebhookRateLimit = 3
	cfg.WebhookRateWindow = time.Minute
	cfg.WorkDir = t.TempDir()

	// A non-nil worker is all the route needs to mount; an unknown-app delivery
	// 404s long before the worker is ever consulted, so a no-op worker is fine.
	worker := deploy.NewWorker(nil, nil, 0, 1, nil)
	srv, err := server.New(t.Context(), cfg, s, worker, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler

	// Burn the budget: each unknown-app delivery is a clean 404.
	for i := 0; i < cfg.WebhookRateLimit; i++ {
		rr, _ := do(t, h, "POST", "/webhooks/no-such-app", nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("delivery %d: code=%d, want 404", i+1, rr.Code)
		}
	}

	// Over budget → 429 with Retry-After.
	rr, _ := do(t, h, "POST", "/webhooks/no-such-app", nil)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget delivery: code=%d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on 429")
	}
}
