//go:build integration

package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/server"
)

// TestLoginIsRateLimited verifies the plan's exit criterion at the route
// level: 6th failed login within the window returns 429 with Retry-After.
// Uses a real Postgres + the production route stack so middleware ordering
// is also covered.
func TestLoginIsRateLimited(t *testing.T) {
	s := setupPool(t)
	cfg := config.Default()
	cfg.LoginRateLimit = 5
	cfg.LoginRateWindow = 15 * time.Minute
	srv, err := server.New(t.Context(), cfg, s, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler

	// Create an admin to make the login surface real (otherwise every attempt
	// hits the dummy-hash path; same code branch, but this keeps the test
	// honest about the production path).
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin: %d", rr.Code)
	}

	// Setup-admin itself counts against the budget, so we have 4 tokens left.
	// Burn 4 wrong-password attempts — each 401.
	for i := 0; i < 4; i++ {
		rr, _ := do(t, h, "POST", "/api/auth/login", map[string]any{
			"username": "alice",
			"password": "wrong",
		})
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code=%d, want 401", i+1, rr.Code)
		}
	}

	// 6th request total → 429 with Retry-After.
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "alice",
		"password": "wrong",
	})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget request: code=%d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on 429")
	}
}
