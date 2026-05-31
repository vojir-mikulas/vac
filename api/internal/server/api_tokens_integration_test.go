//go:build integration

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestAPITokenLifecycle(t *testing.T) {
	h, cfg := setupServer(t)

	// 1. Bootstrap admin + log in.
	rr, _ := bootstrapAdmin(t, h, cfg, "alice", "swordfish-pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup admin: %d", rr.Code)
	}
	cookies := loginAs(t, h, "alice", "swordfish-pw", "10.0.0.1")
	csrf := findCookie(auth.CSRFCookie, cookies)

	// 2. Create a token via cookie auth.
	req := reqWithCookies(t, "POST", "/api/auth/api-tokens", map[string]any{
		"name":            "cli",
		"expires_in_days": 30,
	}, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token: %d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	mustJSON(t, rr.Body.Bytes(), &created)
	raw, _ := created["token"].(string)
	if !strings.HasPrefix(raw, auth.TokenPrefix) {
		t.Fatalf("token %q does not start with %q", raw, auth.TokenPrefix)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create response missing id")
	}

	// 3. /me via Bearer, no cookies, no CSRF — must succeed.
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/me via bearer: %d body=%s", rr.Code, rr.Body.String())
	}
	var me map[string]any
	mustJSON(t, rr.Body.Bytes(), &me)
	if me["username"] != "alice" {
		t.Errorf("/me username via bearer = %v, want alice", me["username"])
	}

	// 4. Bearer + mutating request: no CSRF header should be required.
	// Use create-another-token as the mutating call.
	body, _ := json.Marshal(map[string]any{"name": "another"})
	req = httptest.NewRequest("POST", "/api/auth/api-tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+raw)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create via bearer (no csrf): %d body=%s", rr.Code, rr.Body.String())
	}

	// 5. List tokens via cookie — should see two.
	req = reqWithCookies(t, "GET", "/api/auth/api-tokens", nil, cookies)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tokens: %d", rr.Code)
	}
	var list []map[string]any
	mustJSON(t, rr.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("token count = %d, want 2", len(list))
	}
	for _, tok := range list {
		if _, has := tok["token"]; has {
			t.Errorf("list response leaked raw token field: %+v", tok)
		}
	}

	// 6. Revoke the original token via cookie auth.
	req = reqWithCookies(t, "DELETE", "/api/auth/api-tokens/"+id, nil, cookies)
	req.Header.Set(auth.CSRFHeader, csrf.Value)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d body=%s", rr.Code, rr.Body.String())
	}

	// 7. Bearer call with the revoked token now 401s.
	req = httptest.NewRequest("GET", "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("/me with revoked bearer = %d, want 401", rr.Code)
	}
}

func TestAPITokenExpired(t *testing.T) {
	// Drive the store directly to plant an already-expired token, then
	// confirm the Bearer path rejects it. This exercises the expires_at
	// branch without sleeping in the test.
	s := setupPool(t)

	ctx := context.Background()
	u, err := s.CreateUser(ctx, "carol", mustBcrypt(t, "swordfish-pw"))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	tm := auth.NewTokenManager(s)
	past := time.Now().Add(-time.Hour)
	raw, _, err := tm.Create(ctx, u.ID, "stale", &past)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	_, _, err = tm.Lookup(ctx, raw)
	if err == nil {
		t.Fatal("expected lookup to fail for expired token")
	}
	if err != auth.ErrTokenExpired {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
}

// mustBcrypt creates a stored-shape password hash. Used by tests that go
// straight to the store and bypass setup-admin.
func mustBcrypt(t *testing.T, plain string) string {
	t.Helper()
	h, err := auth.HashPassword(plain)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return h
}

func TestAPITokenCannotBeUsedByOtherUser(t *testing.T) {
	s := setupPool(t)
	ctx := context.Background()

	owner, err := s.CreateUser(ctx, "owner", mustBcrypt(t, "x"))
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	other, err := s.CreateUser(ctx, "other", mustBcrypt(t, "x"))
	if err != nil {
		t.Fatalf("other: %v", err)
	}
	tm := auth.NewTokenManager(s)
	_, tok, err := tm.Create(ctx, owner.ID, "k", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := tm.Revoke(ctx, other.ID, tok.ID); err == nil {
		t.Fatal("other user should not be able to revoke owner's token")
	} else if err != store.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}

	// Owner can revoke it just fine.
	if err := tm.Revoke(ctx, owner.ID, tok.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
}
