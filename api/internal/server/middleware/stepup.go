package middleware

import (
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// RequireStepUp guards destructive routes with a fresh re-authentication. The
// request must run on a session that re-proved the user's identity within
// auth.StepUpTTL — a fresh authenticator/recovery code when TOTP is enabled, or a
// password re-entry when it isn't (see handler.StepUp) — otherwise it is rejected
// with 403 / step_up_required so the client can prompt and retry.
//
// Pass-through case (the gate is a no-op):
//   - API-token auth: a bearer token is a deliberately-created standing
//     credential, treated as already elevated; the interactive step-up flow does
//     not apply to non-browser clients.
//
// Note the gate is NOT waived when TOTP is disabled: an account without a second
// factor is the weakest posture, so destructive ops there still demand a fresh
// password — otherwise a hijacked session cookie alone would suffice for, e.g.,
// exporting the master key via the migration bundle.
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
		// Non-browser (bearer token): already elevated, nothing to step up against.
		if auth.APIToken(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		sess := auth.Session(r.Context())
		if sess == nil || !auth.StepUpFresh(*sess) {
			audit.Action(r.Context(), "stepup.required", nil)
			handler.WriteErrorCode(w, http.StatusForbidden, handler.CodeStepUpRequired,
				"re-verification required for this action")
			return
		}
		next.ServeHTTP(w, r)
	})
}
