package auth

const (
	// SessionCookie is the HttpOnly cookie carrying the raw session token.
	SessionCookie = "vac_session"

	// PreAuthCookie is the HttpOnly cookie carrying a short-lived token for
	// users who have passed the password step but still owe a TOTP code.
	// Distinct name so JS / middleware never confuse a partial login with a
	// real one.
	PreAuthCookie = "vac_pre"

	// CSRFCookie is the non-HttpOnly cookie used for the double-submit CSRF
	// pattern. JS reads it and echoes it back via CSRFHeader.
	CSRFCookie = "vac_csrf"

	// GuardCookie is the HttpOnly, host-scoped cookie proving a visitor cleared
	// the VAC login gate for one guarded app. It is NOT the control-plane session
	// (vac_session): a distinct token, bound to a single host, that grants access
	// only to that app — never the dashboard. See internal/guard.
	GuardCookie = "vac_guard"

	// CSRFHeader is the request header that must match CSRFCookie on
	// session-authenticated mutating requests.
	CSRFHeader = "X-CSRF-Token"
)
