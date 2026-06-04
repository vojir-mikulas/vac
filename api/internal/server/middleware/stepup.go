package middleware

import (
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// RequireStepUp guards destructive routes with fresh 2FA. When the user has TOTP
// enabled, the request must run on a session that re-proved its second factor
// within auth.StepUpTTL — otherwise it is rejected with 403 / step_up_required
// so the client can prompt for a code and retry.
//
// Pass-through cases (the gate is a no-op):
//   - TOTP not enabled: there is no second factor to demand.
//   - API-token auth: a bearer token is a deliberately-created standing
//     credential, treated as already elevated; the interactive step-up flow
//     does not apply to non-browser clients.
//
// Mount it per-route via chi's With(), inside the RequireSession group so the
// user is already resolved.
func RequireStepUp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			handler.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// Non-browser (bearer token) or no second factor configured: nothing to
		// step up against.
		if auth.APIToken(r.Context()) != nil || !u.TOTPEnabled {
			next.ServeHTTP(w, r)
			return
		}
		sess := auth.Session(r.Context())
		if sess == nil || !auth.StepUpFresh(*sess) {
			audit.Describe(r.Context(), "step-up 2FA required")
			handler.WriteErrorCode(w, http.StatusForbidden, handler.CodeStepUpRequired,
				"two-factor re-verification required for this action")
			return
		}
		next.ServeHTTP(w, r)
	})
}
