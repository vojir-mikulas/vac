package auth

const (
	// SessionCookie is the HttpOnly cookie carrying the raw session token.
	SessionCookie = "vac_session"

	// CSRFCookie is the non-HttpOnly cookie used for the double-submit CSRF
	// pattern. JS reads it and echoes it back via CSRFHeader.
	CSRFCookie = "vac_csrf"

	// CSRFHeader is the request header that must match CSRFCookie on
	// session-authenticated mutating requests.
	CSRFHeader = "X-CSRF-Token"
)
