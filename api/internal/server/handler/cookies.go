package handler

import (
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

func setSessionCookie(w http.ResponseWriter, value string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func setCSRFCookie(w http.ResponseWriter, value string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:  auth.CSRFCookie,
		Value: value,
		Path:  "/",
		// Not HttpOnly — JS reads it to echo back in the CSRF header.
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func clearCSRFCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
