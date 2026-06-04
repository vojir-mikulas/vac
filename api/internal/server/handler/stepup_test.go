package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// stepUpReq builds a POST /api/auth/step-up with the given JSON body and an
// injected user/session, mimicking the Auth + RequireSession middleware.
func stepUpReq(body string, u *store.User, sess *store.Session) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/step-up", strings.NewReader(body))
	ctx := req.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	if sess != nil {
		ctx = auth.WithSession(ctx, sess)
	}
	return req.WithContext(ctx)
}

// These guard branches return before the TOTP manager is touched, so a manager
// with no store/box is never dereferenced.
func TestStepUp_NoSession_400(t *testing.T) {
	h := StepUp(nil, auth.NewTOTPManager(nil, nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, stepUpReq(`{"code":"123456"}`, &store.User{ID: "u1", TOTPEnabled: true}, nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestStepUp_TOTPDisabled_400(t *testing.T) {
	h := StepUp(nil, auth.NewTOTPManager(nil, nil))
	rr := httptest.NewRecorder()
	u := &store.User{ID: "u1", TOTPEnabled: false}
	h.ServeHTTP(rr, stepUpReq(`{"code":"123456"}`, u, &store.Session{ID: "s1"}))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestStepUp_MissingCode_400(t *testing.T) {
	h := StepUp(nil, auth.NewTOTPManager(nil, nil))
	rr := httptest.NewRecorder()
	u := &store.User{ID: "u1", TOTPEnabled: true}
	h.ServeHTTP(rr, stepUpReq(`{}`, u, &store.Session{ID: "s1"}))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
