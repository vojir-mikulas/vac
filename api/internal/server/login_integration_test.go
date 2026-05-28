//go:build integration

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

// reqWithCookies copies cookies from prev into a new request so we can simulate
// a browser session across calls.
func reqWithCookies(t *testing.T, method, path string, body any, cookies []*http.Cookie) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func findCookie(name string, cs []*http.Cookie) *http.Cookie {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginFlow(t *testing.T) {
	h := setupServer(t)

	// 1. Create admin via the setup wizard.
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin status = %d", rr.Code)
	}

	// 2. Wrong password → 401.
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "alice",
		"password": "wrong",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", rr.Code)
	}

	// 3. Unknown user → 401 (and indistinguishable from wrong password).
	rr, _ = do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "ghost",
		"password": "anything",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user status = %d, want 401", rr.Code)
	}

	// 4. Correct credentials → 200 with cookies.
	rr, body := do(t, h, "POST", "/api/auth/login", map[string]any{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%v", rr.Code, body)
	}
	if u, _ := body["username"].(string); u != "alice" {
		t.Errorf("login response username = %v", body["username"])
	}

	cookies := rr.Result().Cookies()
	sessionCookie := findCookie(auth.SessionCookie, cookies)
	csrfCookie := findCookie(auth.CSRFCookie, cookies)
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("login did not set session cookie")
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatal("login did not set csrf cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie should be HttpOnly")
	}
	if csrfCookie.HttpOnly {
		t.Error("csrf cookie must NOT be HttpOnly (JS needs to read it)")
	}

	// 5. /me with the session cookie → 200.
	req := reqWithCookies(t, "GET", "/api/auth/me", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/me status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var me map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &me)
	if me["username"] != "alice" {
		t.Errorf("/me username = %v", me["username"])
	}

	// 6. Logout without CSRF header → 403.
	req = reqWithCookies(t, "POST", "/api/auth/logout", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("logout w/o csrf status = %d, want 403", rr.Code)
	}

	// 7. Logout with matching CSRF header → 200.
	req = reqWithCookies(t, "POST", "/api/auth/logout", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrfCookie.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body=%s", rr.Code, rr.Body.String())
	}

	// 8. /me with the now-revoked cookies → 401.
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("/me after logout status = %d, want 401", rr.Code)
	}
}

func TestMeRequiresSession(t *testing.T) {
	h := setupServer(t)
	rr, _ := do(t, h, "GET", "/api/auth/me", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("/me without session = %d, want 401", rr.Code)
	}
}
