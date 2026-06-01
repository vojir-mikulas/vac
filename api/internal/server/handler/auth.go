package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// dummyHash is a bcrypt hash used to keep "user does not exist" timing
// indistinguishable from "wrong password" — without it, attackers can
// enumerate usernames by measuring login response time.
var (
	dummyHashOnce sync.Once
	dummyHash     string
)

func ensureDummyHash() {
	dummyHashOnce.Do(func() {
		h, err := auth.HashPassword("vac-username-enumeration-mitigation")
		if err == nil {
			dummyHash = h
		}
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

type meResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	TOTPEnabled bool   `json:"totp_enabled"`
}

type totpRequiredResponse struct {
	TOTPRequired bool `json:"totp_required"`
}

// Login verifies the password and issues a session + CSRF cookie pair. If the
// user has TOTP enabled, the response is a `{"totp_required": true}` plus a
// short-lived pre-auth cookie; the client must then POST /api/auth/totp with a
// code to finish authenticating.
func Login(s *store.Store, sm *auth.SessionManager, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Username == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, "username and password are required")
			return
		}

		ensureDummyHash()

		user, err := s.GetUserByUsername(r.Context(), req.Username)
		if err != nil {
			// Run bcrypt against a dummy hash so the response timing matches
			// a real verify — prevents username enumeration via timing.
			_ = auth.VerifyPassword(dummyHash, req.Password)
			auditAuthFailure(r, "unknown_user", req.Username, "")
			WriteErrorCode(w, http.StatusUnauthorized, CodeInvalidCredentials, "invalid credentials")
			return
		}
		if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
			auditAuthFailure(r, "bad_password", req.Username, user.ID)
			WriteErrorCode(w, http.StatusUnauthorized, CodeInvalidCredentials, "invalid credentials")
			return
		}

		ip := clientIP(r)

		if user.TOTPEnabled {
			preToken, _, err := sm.CreatePreAuth(r.Context(), user.ID, ip, r.UserAgent())
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "could not create pre-auth session")
				return
			}
			setPreAuthCookie(w, r, preToken, auth.PreAuthTTL)
			// Carry the "remember me" preference through the pre-auth step
			// so the eventual full session honours it.
			http.SetCookie(w, &http.Cookie{
				Name:     "vac_pre_remember",
				Value:    strconv.FormatBool(req.Remember),
				Path:     "/",
				HttpOnly: true,
				Secure:   secureForRequest(r),
				SameSite: http.SameSiteStrictMode,
				MaxAge:   int(auth.PreAuthTTL.Seconds()),
			})
			WriteJSON(w, http.StatusOK, totpRequiredResponse{TOTPRequired: true})
			return
		}

		issueFullSession(w, r, sm, user, req.Remember)
	}
}

// issueFullSession is the tail of login (and TOTP step) shared between the
// password-only path and the password+TOTP path.
func issueFullSession(w http.ResponseWriter, r *http.Request, sm *auth.SessionManager, user store.User, remember bool) {
	ip := clientIP(r)
	token, _, err := sm.Create(r.Context(), user.ID, ip, r.UserAgent(), remember)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	csrf, err := newCSRFToken()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not generate csrf token")
		return
	}
	ttl := sm.TTL(remember)
	setSessionCookie(w, r, token, ttl)
	setCSRFCookie(w, r, csrf, ttl)
	WriteJSON(w, http.StatusOK, meResponse{
		ID:          user.ID,
		Username:    user.Username,
		TOTPEnabled: user.TOTPEnabled,
	})
}

// Logout revokes the current session and clears its cookies. Must be mounted
// behind RequireSession.
func Logout(sm *auth.SessionManager, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := auth.Session(r.Context())
		if sess == nil {
			WriteError(w, http.StatusUnauthorized, "no active session")
			return
		}
		if err := sm.Revoke(r.Context(), sess.ID); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not revoke session")
			return
		}
		clearSessionCookie(w, r)
		clearCSRFCookie(w, r)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
	}
}

// Me returns the authenticated user. Must be mounted behind RequireSession.
func Me(w http.ResponseWriter, r *http.Request) {
	u := auth.User(r.Context())
	if u == nil {
		WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	WriteJSON(w, http.StatusOK, meResponse{
		ID:          u.ID,
		Username:    u.Username,
		TOTPEnabled: u.TOTPEnabled,
	})
}

func clientIP(r *http.Request) *netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return &addr
	}
	return nil
}

func newCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("rand")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// auditAuthFailure logs failed login / TOTP attempts at WARN so they're picked
// up by log shippers without needing a dedicated audit pipeline yet. Reason is
// a short machine-readable tag; the username is logged but never the password
// or any TOTP material.
func auditAuthFailure(r *http.Request, reason, username, userID string) {
	slog.Warn("auth: failed attempt",
		"reason", reason,
		"username", username,
		"user_id", userID,
		"ip", remoteIPString(r),
		"user_agent", r.UserAgent(),
	)
}

func remoteIPString(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
