package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeControlChecker lets CaddyAsk answer 200 for a known control host without
// reaching the store, so the token-gate branches can be exercised in isolation.
type fakeControlChecker struct{ host string }

func (f fakeControlChecker) IsControlDomain(host string) bool { return host == f.host }

// The token gate runs before any store access, so a nil store is never touched
// on the rejection paths.
func TestCaddyAsk_TokenGate(t *testing.T) {
	const token = "s3cr3t-ask-token"
	ctrl := fakeControlChecker{host: "vac.example.com"}

	tests := []struct {
		name       string
		token      string // configured token ("" = gate disabled)
		query      string // raw query string on the request
		header     string // X-Caddy-Ask-Token header value
		wantStatus int
	}{
		{
			name:       "missing token rejected",
			token:      token,
			query:      "domain=vac.example.com",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "wrong query token rejected",
			token:      token,
			query:      "domain=vac.example.com&token=nope",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "correct query token allowed",
			token:      token,
			query:      "domain=vac.example.com&token=" + token,
			wantStatus: http.StatusOK,
		},
		{
			name:       "correct header token allowed",
			token:      token,
			query:      "domain=vac.example.com",
			header:     token,
			wantStatus: http.StatusOK,
		},
		{
			name:       "disabled gate allows without token",
			token:      "",
			query:      "domain=vac.example.com",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := CaddyAsk(nil, tc.token, ctrl, nil)
			req := httptest.NewRequest(http.MethodGet, "/internal/caddy/ask?"+tc.query, nil)
			if tc.header != "" {
				req.Header.Set("X-Caddy-Ask-Token", tc.header)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
