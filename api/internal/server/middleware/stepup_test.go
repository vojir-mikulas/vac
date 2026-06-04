package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// reqWith builds a DELETE request whose context carries the given user/session/
// token, mimicking what the Auth middleware would have injected.
func reqWith(u *store.User, sess *store.Session, tok *store.APIToken) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/apps/x", nil)
	ctx := req.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	if sess != nil {
		ctx = auth.WithSession(ctx, sess)
	}
	if tok != nil {
		ctx = auth.WithAPIToken(ctx, tok)
	}
	return req.WithContext(ctx)
}

func stepUpStatus(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	RequireStepUp(okHandler()).ServeHTTP(rr, req)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body.Code
}

func TestRequireStepUp_NoUser_401(t *testing.T) {
	code, _ := stepUpStatus(t, reqWith(nil, nil, nil))
	if code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

func TestRequireStepUp_TOTPDisabled_PassesThrough(t *testing.T) {
	u := &store.User{ID: "u1", TOTPEnabled: false}
	code, _ := stepUpStatus(t, reqWith(u, &store.Session{ID: "s1"}, nil))
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
}

func TestRequireStepUp_APIToken_PassesThrough(t *testing.T) {
	u := &store.User{ID: "u1", TOTPEnabled: true}
	tok := &store.APIToken{UserID: "u1"}
	// No session, stale by every measure — but bearer auth bypasses the gate.
	code, _ := stepUpStatus(t, reqWith(u, nil, tok))
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
}

func TestRequireStepUp_NeverVerified_403(t *testing.T) {
	u := &store.User{ID: "u1", TOTPEnabled: true}
	sess := &store.Session{ID: "s1"} // StepUpVerifiedAt nil
	code, errCode := stepUpStatus(t, reqWith(u, sess, nil))
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
	if errCode != handler.CodeStepUpRequired {
		t.Errorf("code = %q, want %q", errCode, handler.CodeStepUpRequired)
	}
}

func TestRequireStepUp_Fresh_PassesThrough(t *testing.T) {
	now := time.Now()
	u := &store.User{ID: "u1", TOTPEnabled: true}
	sess := &store.Session{ID: "s1", StepUpVerifiedAt: &now}
	code, _ := stepUpStatus(t, reqWith(u, sess, nil))
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
}

func TestRequireStepUp_Stale_403(t *testing.T) {
	old := time.Now().Add(-auth.StepUpTTL - time.Minute)
	u := &store.User{ID: "u1", TOTPEnabled: true}
	sess := &store.Session{ID: "s1", StepUpVerifiedAt: &old}
	code, errCode := stepUpStatus(t, reqWith(u, sess, nil))
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
	if errCode != handler.CodeStepUpRequired {
		t.Errorf("code = %q, want %q", errCode, handler.CodeStepUpRequired)
	}
}
