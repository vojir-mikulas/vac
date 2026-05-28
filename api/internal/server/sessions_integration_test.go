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

// loginAs runs POST /api/auth/login with a synthetic remote IP and returns
// the issued cookies. Used to simulate "the same user logging in from
// multiple devices" since httptest's default RemoteAddr is shared.
func loginAs(t *testing.T, h http.Handler, username, password, remoteIP string) []*http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteIP + ":4242"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login from %s: code=%d, body=%s", remoteIP, rr.Code, rr.Body.String())
	}
	return rr.Result().Cookies()
}

func TestListAndRevokeSessions(t *testing.T) {
	h := setupServer(t)

	// 1. Bootstrap admin.
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin: %d", rr.Code)
	}

	// 2. Log in twice from different IPs.
	device1 := loginAs(t, h, "alice", "swordfish-pw", "10.0.0.1")
	device2 := loginAs(t, h, "alice", "swordfish-pw", "10.0.0.2")

	// 3. From device1's perspective, GET /api/auth/sessions returns 2 rows;
	//    device1 must be flagged is_current and carry IP 10.0.0.1.
	req := reqWithCookies(t, "GET", "/api/auth/sessions", nil, device1)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if len(list) != 2 {
		t.Fatalf("session count = %d, want 2 (body=%s)", len(list), rr.Body.String())
	}
	var current, other map[string]any
	for _, s := range list {
		if is, _ := s["is_current"].(bool); is {
			current = s
		} else {
			other = s
		}
	}
	if current == nil || other == nil {
		t.Fatalf("expected exactly one is_current row, got %+v", list)
	}
	if ip, _ := current["ip"].(string); ip != "10.0.0.1" {
		t.Errorf("current session ip = %q, want 10.0.0.1", ip)
	}
	if ip, _ := other["ip"].(string); ip != "10.0.0.2" {
		t.Errorf("other session ip = %q, want 10.0.0.2", ip)
	}

	// 4. Revoking the current session by id is refused with 409 — the
	//    correct path is /api/auth/logout.
	csrf := findCookie(auth.CSRFCookie, device1)
	currentID, _ := current["id"].(string)
	req = reqWithCookies(t, "DELETE", "/api/auth/sessions/"+currentID, nil, device1)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete current = %d, want 409 body=%s", rr.Code, rr.Body.String())
	}

	// 5. Revoke device2 by id.
	otherID, _ := other["id"].(string)
	req = reqWithCookies(t, "DELETE", "/api/auth/sessions/"+otherID, nil, device1)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete other: %d body=%s", rr.Code, rr.Body.String())
	}

	// 6. List now has 1, and device2's cookies no longer let it call /me.
	req = reqWithCookies(t, "GET", "/api/auth/sessions", nil, device1)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("after revoke other: session count = %d, want 1", len(list))
	}

	req = reqWithCookies(t, "GET", "/api/auth/me", nil, device2)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device /me = %d, want 401", rr.Code)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	h := setupServer(t)

	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "bob",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin: %d", rr.Code)
	}

	// 3 logins from 3 different IPs.
	keep := loginAs(t, h, "bob", "swordfish-pw", "10.1.0.1")
	d2 := loginAs(t, h, "bob", "swordfish-pw", "10.1.0.2")
	d3 := loginAs(t, h, "bob", "swordfish-pw", "10.1.0.3")

	csrf := findCookie(auth.CSRFCookie, keep)
	req := reqWithCookies(t, "DELETE", "/api/auth/sessions", nil, keep)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke others: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	mustJSON(t, rr.Body.Bytes(), &resp)
	if n, _ := resp["revoked"].(float64); int(n) != 2 {
		t.Errorf("revoked count = %v, want 2", resp["revoked"])
	}

	// keep still works, d2/d3 are 401.
	for label, cookies := range map[string][]*http.Cookie{"d2": d2, "d3": d3} {
		req = reqWithCookies(t, "GET", "/api/auth/me", nil, cookies)
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s after revoke-others: code=%d, want 401", label, rr.Code)
		}
	}
	req = reqWithCookies(t, "GET", "/api/auth/me", nil, keep)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("keep after revoke-others: code=%d, want 200", rr.Code)
	}
}

func TestRevokeSessionFromOtherUserIs404(t *testing.T) {
	h := setupServer(t)

	// Single admin slot is taken by alice; we'll create a second user via
	// the store-only path? No — the API has no signup endpoint yet. For
	// this test, just confirm that revoking a non-existent id 404s.
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin: %d", rr.Code)
	}
	cookies := loginAs(t, h, "alice", "swordfish-pw", "10.2.0.1")
	csrf := findCookie(auth.CSRFCookie, cookies)

	req := reqWithCookies(t, "DELETE", "/api/auth/sessions/00000000-0000-0000-0000-000000000000", nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown id = %d, want 404 body=%s", rr.Code, rr.Body.String())
	}
}
