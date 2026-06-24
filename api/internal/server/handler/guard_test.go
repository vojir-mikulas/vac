package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/guard"
)

func guardKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 7)
	}
	return k
}

type fakeGuardHosts struct {
	guarded map[string]bool
	err     error
}

func (f fakeGuardHosts) IsGuardedHost(_ context.Context, host string) (bool, error) {
	return f.guarded[strings.ToLower(host)], f.err
}

func verifyReq(host, uri, token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, guardVerifyPath, nil)
	r.Header.Set("X-Caddy-Ask-Token", token)
	r.Header.Set("X-Vac-Guard-Host", host)
	r.Header.Set("X-Vac-Guard-Uri", uri)
	return r
}

func TestGuardVerify_ValidCookieAllows(t *testing.T) {
	signer := guard.New(guardKey())
	h := GuardVerify(signer, "vac.example.com", "sekret")

	r := verifyReq("tool.example.com", "/dashboard", "sekret")
	r.AddCookie(&http.Cookie{
		Name:  auth.GuardCookie,
		Value: signer.Mint(guard.KindSession, "tool.example.com", "alice", time.Hour),
	})
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("X-Vac-User"); got != "alice" {
		t.Errorf("X-Vac-User = %q, want alice", got)
	}
}

func TestGuardVerify_AnonymousBouncesToPortal(t *testing.T) {
	signer := guard.New(guardKey())
	h := GuardVerify(signer, "vac.example.com", "sekret")

	w := httptest.NewRecorder()
	h(w, verifyReq("tool.example.com", "/deep/path?x=1", "sekret"))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://vac.example.com"+guardStartPath+"?rd=") {
		t.Fatalf("Location = %q", loc)
	}
	u, _ := url.Parse(loc)
	if rd := u.Query().Get("rd"); rd != "https://tool.example.com/deep/path?x=1" {
		t.Errorf("rd = %q", rd)
	}
}

func TestGuardVerify_CallbackMintsCookie(t *testing.T) {
	signer := guard.New(guardKey())
	h := GuardVerify(signer, "vac.example.com", "sekret")

	xchg := signer.Mint(guard.KindExchange, "tool.example.com", "bob", time.Minute)
	uri := guardCallbackPath + "?token=" + url.QueryEscape(xchg) + "&rd=" + url.QueryEscape("https://tool.example.com/secret")
	w := httptest.NewRecorder()
	h(w, verifyReq("tool.example.com", uri, "sekret"))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://tool.example.com/secret" {
		t.Errorf("Location = %q", loc)
	}
	var set *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.GuardCookie {
			set = c
		}
	}
	if set == nil {
		t.Fatal("no guard cookie set")
	}
	if set.SameSite != http.SameSiteLaxMode || !set.HttpOnly {
		t.Errorf("cookie attrs: samesite=%v httponly=%v", set.SameSite, set.HttpOnly)
	}
	user, ok := signer.Verify(guard.KindSession, "tool.example.com", set.Value)
	if !ok || user != "bob" {
		t.Errorf("minted cookie verify: user=%q ok=%v", user, ok)
	}
}

func TestGuardVerify_StaleExchangeRestartsDance(t *testing.T) {
	signer := guard.New(guardKey())
	h := GuardVerify(signer, "vac.example.com", "sekret")

	// Exchange token bound to a different host → invalid here.
	bad := signer.Mint(guard.KindExchange, "other.example.com", "bob", time.Minute)
	uri := guardCallbackPath + "?token=" + url.QueryEscape(bad) + "&rd=" + url.QueryEscape("https://tool.example.com/secret")
	w := httptest.NewRecorder()
	h(w, verifyReq("tool.example.com", uri, "sekret"))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "https://vac.example.com"+guardStartPath) {
		t.Errorf("should restart at portal, Location = %q", loc)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.GuardCookie {
			t.Error("must not set a cookie for a bad exchange token")
		}
	}
}

func TestGuardVerify_RejectsWrongAskToken(t *testing.T) {
	signer := guard.New(guardKey())
	h := GuardVerify(signer, "vac.example.com", "sekret")
	w := httptest.NewRecorder()
	h(w, verifyReq("tool.example.com", "/", "wrong"))
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestGuardVerify_FailsClosedWithoutSigner(t *testing.T) {
	h := GuardVerify(nil, "vac.example.com", "sekret")
	w := httptest.NewRecorder()
	h(w, verifyReq("tool.example.com", "/", "sekret"))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestGuardStart_RefusesNonGuardedHost(t *testing.T) {
	signer := guard.New(guardKey())
	chk := fakeGuardHosts{guarded: map[string]bool{}}
	h := GuardStart(nil, signer, chk)

	r := httptest.NewRequest(http.MethodGet, guardStartPath+"?rd="+url.QueryEscape("https://evil.example.com/"), nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestGuardStart_InvalidRedirect(t *testing.T) {
	signer := guard.New(guardKey())
	chk := fakeGuardHosts{guarded: map[string]bool{}}
	h := GuardStart(nil, signer, chk)

	r := httptest.NewRequest(http.MethodGet, guardStartPath+"?rd="+url.QueryEscape("http://tool.example.com/"), nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-https rd: status = %d, want 400", w.Code)
	}
}

func TestGuardStart_NoSessionGoesToLogin(t *testing.T) {
	signer := guard.New(guardKey())
	chk := fakeGuardHosts{guarded: map[string]bool{"tool.example.com": true}}
	// No session cookie on the request, so the session manager is never consulted.
	sm := auth.NewSessionManager(nil, time.Hour, time.Hour)
	h := GuardStart(sm, signer, chk)

	rd := "https://tool.example.com/secret"
	r := httptest.NewRequest(http.MethodGet, guardStartPath+"?rd="+url.QueryEscape(rd), nil)
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("Location = %q, want /login?next=", loc)
	}
	u, _ := url.Parse(loc)
	next := u.Query().Get("next")
	if !strings.HasPrefix(next, guardStartPath+"?rd=") || !strings.Contains(next, url.QueryEscape(rd)) {
		t.Errorf("next = %q", next)
	}
}
