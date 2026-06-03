package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/config"
)

// A password that is within the 72-RUNE validator bound but over the 72-BYTE
// bcrypt ceiling must be rejected with a clear 400 — not silently truncated and
// not a generic hashing 500. This exercises the explicit byte guard, which the
// rune-counting `max=72` validator tag alone would miss for multi-byte input.
// The check runs before the store is touched, so a nil store is safe here.
func TestSetupAdmin_RejectsOverlongMultibytePassword(t *testing.T) {
	// 40 × "é" (2 bytes each) = 80 bytes but only 40 runes: passes max=72 runes,
	// fails the 72-byte ceiling.
	password := strings.Repeat("é", 40)
	body := `{"username":"alice","password":"` + password + `","setup_token":"x"}`

	req := httptest.NewRequest(http.MethodPost, "/api/setup/admin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	SetupAdmin(nil, nil, config.Config{})(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "72 bytes") {
		t.Errorf("expected a 72-byte message, got: %s", rr.Body.String())
	}
}
