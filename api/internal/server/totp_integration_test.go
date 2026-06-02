//go:build integration

package server_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcwait "github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/server"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(b))
	}
}

// setupServerWithKey is the same as setupServer but also wires a real master
// key — required for TOTP, which encrypts secrets at rest.
func setupServerWithKey(t *testing.T) (http.Handler, config.Config) {
	t.Helper()
	ctx := context.Background()

	pgC, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("vac"),
		postgres.WithUsername("vac"),
		postgres.WithPassword("vac"),
		testcontainers.WithWaitStrategy(
			tcwait.ForLog("database system is ready to accept connections").
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

	cfg := config.Default()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	cfg.MasterKey = key
	// Same reason as setupServer — the 5/15min default would throttle the
	// multi-step 2FA flows in this file.
	cfg.LoginRateLimit = 100
	cfg.LoginRateWindow = time.Minute
	// Each test gets its own work dir so the setup token file is isolated.
	cfg.WorkDir = t.TempDir()
	// Default exposure is "public" which sets Secure cookies; httptest
	// requests are not HTTPS, but Go's cookie jar still records them.
	// The login_integration_test already relies on this.

	srv, err := server.New(t.Context(), cfg, store.New(pool), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if _, err := auth.EnsureSetupToken(cfg.WorkDir); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	return srv.Handler, cfg
}

func TestTOTPSetupAndLoginFlow(t *testing.T) {
	h, cfg := setupServerWithKey(t)

	// 1. Create admin + log in.
	rr, _ := bootstrapAdmin(t, h, cfg, "alice", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup admin: %d", rr.Code)
	}

	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	csrf := findCookie(auth.CSRFCookie, cookies)
	if csrf == nil {
		t.Fatal("missing csrf cookie after login")
	}

	// 2. POST /api/auth/totp/setup → get otpauth URI + secret.
	req := reqWithCookies(t, "POST", "/api/auth/totp/setup", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("totp setup: %d body=%s", rr.Code, rr.Body.String())
	}
	var setupBody map[string]any
	mustJSON(t, rr.Body.Bytes(), &setupBody)
	secret, _ := setupBody["secret"].(string)
	if secret == "" {
		t.Fatal("setup response missing secret")
	}

	// 3. Generate the current code from that secret, POST /enable → recovery codes.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	req = reqWithCookies(t, "POST", "/api/auth/totp/enable", map[string]string{"code": code}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("totp enable: %d body=%s", rr.Code, rr.Body.String())
	}
	var enableBody map[string]any
	mustJSON(t, rr.Body.Bytes(), &enableBody)
	recoveryCodes, _ := enableBody["recovery_codes"].([]any)
	if len(recoveryCodes) != 10 {
		t.Fatalf("recovery_codes count = %d, want 10", len(recoveryCodes))
	}

	// 4. /me should reflect totp_enabled = true.
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var me map[string]any
	mustJSON(t, rr.Body.Bytes(), &me)
	if e, _ := me["totp_enabled"].(bool); !e {
		t.Errorf("/me totp_enabled = %v, want true", me["totp_enabled"])
	}

	// 5. Logout — must re-authenticate via 2FA from here.
	req = reqWithCookies(t, "POST", "/api/auth/logout", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout: %d", rr.Code)
	}

	// 6. Login with password only → 200 with totp_required and pre-auth cookie.
	rr, body := do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("password login w/ totp on: %d", rr.Code)
	}
	if req, _ := body["totp_required"].(bool); !req {
		t.Fatalf("login response did not signal totp_required: %v", body)
	}
	preAuthCookies := rr.Result().Cookies()
	if findCookie(auth.PreAuthCookie, preAuthCookies) == nil {
		t.Fatal("login did not set pre-auth cookie")
	}
	if findCookie(auth.SessionCookie, preAuthCookies) != nil {
		t.Fatal("login should not set full session cookie when totp is required")
	}

	// 7. /me with the pre-auth cookie alone → 401 (pre-auth is not a real session).
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("/me with pre-auth only = %d, want 401", rr.Code)
	}

	// 8. Submit a wrong code → 401, pre-auth cookie still valid.
	req = reqWithCookies(t, "POST", "/api/auth/totp", map[string]string{"code": "000000"}, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong totp code = %d, want 401", rr.Code)
	}

	// 9. Submit the correct code → 200, full session + csrf cookies.
	code, err = totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	req = reqWithCookies(t, "POST", "/api/auth/totp", map[string]string{"code": code}, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("correct totp code: %d body=%s", rr.Code, rr.Body.String())
	}
	fullCookies := rr.Result().Cookies()
	if findCookie(auth.SessionCookie, fullCookies) == nil {
		t.Fatal("totp success did not set session cookie")
	}
	if findCookie(auth.CSRFCookie, fullCookies) == nil {
		t.Fatal("totp success did not set csrf cookie")
	}

	// 10. /me with the full session → 200.
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, fullCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/me after totp = %d, body=%s", rr.Code, rr.Body.String())
	}

	// 11. Pre-auth cookie has been burned — replaying it fails.
	req = reqWithCookies(t, "POST", "/api/auth/totp", map[string]string{"code": code}, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("pre-auth replay = %d, want 401", rr.Code)
	}
}

func TestTOTPRecoveryCode(t *testing.T) {
	h, cfg := setupServerWithKey(t)

	// Setup admin, enable TOTP — same as TestTOTPSetupAndLoginFlow steps 1-3.
	rr, _ := bootstrapAdmin(t, h, cfg, "bob", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup admin: %d", rr.Code)
	}
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "bob",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	csrf := findCookie(auth.CSRFCookie, cookies)

	req := reqWithCookies(t, "POST", "/api/auth/totp/setup", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var setupBody map[string]any
	mustJSON(t, rr.Body.Bytes(), &setupBody)
	secret, _ := setupBody["secret"].(string)

	code, _ := totp.GenerateCode(secret, time.Now())
	req = reqWithCookies(t, "POST", "/api/auth/totp/enable", map[string]string{"code": code}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var enableBody map[string]any
	mustJSON(t, rr.Body.Bytes(), &enableBody)
	codes, _ := enableBody["recovery_codes"].([]any)
	if len(codes) == 0 {
		t.Fatal("no recovery codes returned")
	}
	first, _ := codes[0].(string)
	if first == "" {
		t.Fatal("first recovery code is empty")
	}

	// New login → totp_required.
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "bob",
		"password": "swordfish-pw",
	})
	preAuthCookies := rr.Result().Cookies()

	// Recovery code → 200.
	req = reqWithCookies(t, "POST", "/api/auth/totp", map[string]string{"recovery_code": first}, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("recovery code login: %d body=%s", rr.Code, rr.Body.String())
	}

	// Replaying the same recovery code on a fresh pre-auth → 401.
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "bob",
		"password": "swordfish-pw",
	})
	preAuthCookies = rr.Result().Cookies()
	req = reqWithCookies(t, "POST", "/api/auth/totp", map[string]string{"recovery_code": first}, preAuthCookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code replay = %d, want 401", rr.Code)
	}
}

func TestTOTPDisableRequiresPassword(t *testing.T) {
	h, cfg := setupServerWithKey(t)

	rr, _ := bootstrapAdmin(t, h, cfg, "carol", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup admin: %d", rr.Code)
	}
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "carol",
		"password": "swordfish-pw",
	})
	cookies := rr.Result().Cookies()
	csrf := findCookie(auth.CSRFCookie, cookies)

	// Set up + enable TOTP.
	req := reqWithCookies(t, "POST", "/api/auth/totp/setup", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var setupBody map[string]any
	mustJSON(t, rr.Body.Bytes(), &setupBody)
	secret, _ := setupBody["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	req = reqWithCookies(t, "POST", "/api/auth/totp/enable", map[string]string{"code": code}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable: %d", rr.Code)
	}

	// Wrong password → 401, TOTP still enabled.
	req = reqWithCookies(t, "DELETE", "/api/auth/totp", map[string]string{"password": "nope"}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disable w/ wrong pw = %d, want 401", rr.Code)
	}

	// Correct password → 200.
	req = reqWithCookies(t, "DELETE", "/api/auth/totp", map[string]string{"password": "swordfish-pw"}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable: %d body=%s", rr.Code, rr.Body.String())
	}

	// /me reflects totp_enabled = false.
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var me map[string]any
	mustJSON(t, rr.Body.Bytes(), &me)
	if e, _ := me["totp_enabled"].(bool); e {
		t.Errorf("/me totp_enabled = %v, want false", e)
	}
}
