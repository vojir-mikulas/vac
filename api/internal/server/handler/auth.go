package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
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

// Login verifies the password and issues a session + CSRF cookie pair.
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
			WriteError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
			WriteError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		ip := clientIP(r)
		token, sess, err := sm.Create(r.Context(), user.ID, ip, r.UserAgent(), req.Remember)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not create session")
			return
		}

		csrf, err := newCSRFToken()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not generate csrf token")
			return
		}

		ttl := sm.TTL(req.Remember)
		setSessionCookie(w, token, ttl, cfg.SecureCookies())
		setCSRFCookie(w, csrf, ttl, cfg.SecureCookies())

		// Avoid unused-variable warning; sess is here so future callers can
		// surface the session id without another query.
		_ = sess

		WriteJSON(w, http.StatusOK, meResponse{
			ID:          user.ID,
			Username:    user.Username,
			TOTPEnabled: user.TOTPEnabled,
		})
	}
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
		clearSessionCookie(w, cfg.SecureCookies())
		clearCSRFCookie(w, cfg.SecureCookies())
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
