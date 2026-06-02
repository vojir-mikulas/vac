//go:build integration

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/server"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// setupPool spins up Postgres in a container, applies migrations, and returns
// a ready-to-use store. Tests build their own server.New around it.
func setupPool(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	pgC, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("vac"),
		postgres.WithUsername("vac"),
		postgres.WithPassword("vac"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker / postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(ctx) })

	url, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return store.New(pool)
}

func setupServer(t *testing.T) (http.Handler, config.Config) {
	t.Helper()
	s := setupPool(t)
	cfg := config.Default()
	// Tests fire several auth-rated requests back-to-back from one synthetic
	// IP; the 5/15min default would false-positive on otherwise valid flows.
	cfg.LoginRateLimit = 100
	cfg.LoginRateWindow = time.Minute
	// Each test gets its own work dir so the setup token file is isolated.
	cfg.WorkDir = t.TempDir()
	srv, err := server.New(t.Context(), cfg, s, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// Mirror the first-boot bootstrap done in main.go so the setup wizard is
	// usable in tests.
	if _, err := auth.EnsureSetupToken(cfg.WorkDir); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	return srv.Handler, cfg
}

func do(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	}
	return rr, resp
}

// bootstrapAdmin POSTs /api/setup/admin with the on-disk setup token loaded
// from cfg.WorkDir. Tests that need the wizard happy-path use this rather
// than crafting the request themselves.
func bootstrapAdmin(t *testing.T, h http.Handler, cfg config.Config, username, password string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	tok, err := auth.ReadSetupToken(cfg.WorkDir)
	if err != nil {
		t.Fatalf("read setup token: %v", err)
	}
	if tok == "" {
		t.Fatal("no setup token on disk; server.New should have generated one")
	}
	return do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username":    username,
		"password":    password,
		"setup_token": tok,
	})
}

func TestSetupFlow(t *testing.T) {
	h, cfg := setupServer(t)

	// 1. Fresh DB → needs_setup: true, token_required: true
	rr, body := do(t, h, "GET", "/api/setup/status", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rr.Code)
	}
	if needs, _ := body["needs_setup"].(bool); !needs {
		t.Errorf("needs_setup = %v, want true", body["needs_setup"])
	}
	if req, _ := body["token_required"].(bool); !req {
		t.Errorf("token_required = %v, want true", body["token_required"])
	}

	// 2. POST admin → 200 with session cookies (auto sign-in)
	rr, body = bootstrapAdmin(t, h, cfg, "admin", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("create admin status = %d, body=%v", rr.Code, body)
	}
	if u, _ := body["username"].(string); u != "admin" {
		t.Errorf("response username = %v", body["username"])
	}
	if id, _ := body["id"].(string); id == "" {
		t.Error("missing id in response")
	}
	var sawSession, sawCSRF bool
	for _, c := range rr.Result().Cookies() {
		switch c.Name {
		case "vac_session":
			sawSession = true
		case "vac_csrf":
			sawCSRF = true
		}
	}
	if !sawSession || !sawCSRF {
		t.Errorf("expected vac_session + vac_csrf cookies, got %v", rr.Result().Cookies())
	}

	// 3. Status now → needs_setup: false, token_required: false
	_, body = do(t, h, "GET", "/api/setup/status", nil)
	if needs, _ := body["needs_setup"].(bool); needs {
		t.Errorf("needs_setup after admin created = %v, want false", needs)
	}
	if req, _ := body["token_required"].(bool); req {
		t.Errorf("token_required after setup = %v, want false", req)
	}

	// 4. Second POST → 409 conflict (token field still required by validator)
	rr, _ = do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username":    "second",
		"password":    "another-pw-12",
		"setup_token": "anything",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", rr.Code)
	}
}

func TestSetupAdminTokenGate(t *testing.T) {
	h, cfg := setupServer(t)

	// Missing token field → 400 (validator).
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "admin",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("no token status = %d, want 400", rr.Code)
	}

	// Wrong token → 401.
	rr, _ = do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username":    "admin",
		"password":    "swordfish-pw",
		"setup_token": "this-is-not-the-token",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", rr.Code)
	}

	// Correct token still on disk; the bootstrap succeeds.
	rr, _ = bootstrapAdmin(t, h, cfg, "admin", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200", rr.Code)
	}
}

func TestSetupAdminValidation(t *testing.T) {
	h, _ := setupServer(t)

	cases := []struct {
		name string
		body map[string]string
		want int
	}{
		{"missing all", map[string]string{}, http.StatusBadRequest},
		{"empty username", map[string]string{"username": "", "password": "longenoughpw1", "setup_token": "x"}, http.StatusBadRequest},
		{"short password", map[string]string{"username": "u", "password": "short", "setup_token": "x"}, http.StatusBadRequest},
		{"11-char password", map[string]string{"username": "u", "password": "elevenchars", "setup_token": "x"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr, _ := do(t, h, "POST", "/api/setup/admin", tc.body)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}
