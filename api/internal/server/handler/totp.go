package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type totpSetupResponse struct {
	OtpauthURI string `json:"otpauth_uri"`
	Secret     string `json:"secret"`
}

type totpEnableRequest struct {
	Code string `json:"code"`
}

type totpEnableResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

type totpLoginRequest struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

// TOTPSetup starts a TOTP enrolment for the current user. The secret is
// generated, encrypted with the master key, and stored as pending. The user
// must then call TOTPEnable with a code from their authenticator to confirm.
func TOTPSetup(tm *auth.TOTPManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if u.TOTPEnabled {
			WriteError(w, http.StatusConflict, "totp already enabled")
			return
		}
		res, err := tm.Setup(r.Context(), u.ID, u.Username)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not start totp setup")
			return
		}
		WriteJSON(w, http.StatusOK, totpSetupResponse{
			OtpauthURI: res.OtpauthURI,
			Secret:     res.Secret,
		})
	}
}

// TOTPEnable confirms a pending TOTP enrolment and returns one-shot recovery
// codes. The codes are shown exactly once — the server stores only their
// hashes.
func TOTPEnable(tm *auth.TOTPManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var req totpEnableRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Code == "" {
			WriteError(w, http.StatusBadRequest, "code is required")
			return
		}
		codes, err := tm.Enable(r.Context(), u.ID, req.Code)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrTOTPInvalid):
				WriteError(w, http.StatusUnauthorized, "invalid code")
			case errors.Is(err, auth.ErrTOTPDisabled):
				WriteError(w, http.StatusBadRequest, "call /setup before /enable")
			default:
				WriteError(w, http.StatusInternalServerError, "could not enable totp")
			}
			return
		}
		WriteJSON(w, http.StatusOK, totpEnableResponse{RecoveryCodes: codes})
	}
}

// TOTPDisable requires the user to re-prove their password — the same bar as
// changing it — before tearing 2FA back down.
func TOTPDisable(s *store.Store, tm *auth.TOTPManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var req totpDisableRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Password == "" {
			WriteError(w, http.StatusBadRequest, "password is required")
			return
		}
		// Re-fetch to get the password hash (auth.User carries it, but be
		// explicit so we don't drift if the context shape changes).
		full, err := s.GetUserByID(r.Context(), u.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load user")
			return
		}
		if err := auth.VerifyPassword(full.PasswordHash, req.Password); err != nil {
			WriteError(w, http.StatusUnauthorized, "invalid password")
			return
		}
		if err := tm.Disable(r.Context(), u.ID); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not disable totp")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
	}
}

// TOTPLogin is the second factor step. The client arrives carrying the
// pre-auth cookie from POST /api/auth/login; on success the pre-auth session
// is destroyed and a full session is issued.
func TOTPLogin(s *store.Store, sm *auth.SessionManager, tm *auth.TOTPManager, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.PreAuthCookie)
		if err != nil || c.Value == "" {
			WriteError(w, http.StatusUnauthorized, "pre-auth session required")
			return
		}
		sess, user, err := sm.LookupPreAuth(r.Context(), c.Value)
		if err != nil {
			clearPreAuthCookie(w, r)
			WriteError(w, http.StatusUnauthorized, "pre-auth session invalid or expired")
			return
		}

		var req totpLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Code == "" && req.RecoveryCode == "" {
			WriteError(w, http.StatusBadRequest, "code or recovery_code is required")
			return
		}

		if req.Code != "" {
			if err := tm.Verify(r.Context(), user.ID, req.Code); err != nil {
				auditAuthFailure(r, "bad_totp", user.Username, user.ID)
				WriteError(w, http.StatusUnauthorized, "invalid code")
				return
			}
		} else {
			if err := tm.ConsumeRecoveryCode(r.Context(), user.ID, req.RecoveryCode); err != nil {
				auditAuthFailure(r, "bad_recovery_code", user.Username, user.ID)
				WriteError(w, http.StatusUnauthorized, "invalid recovery code")
				return
			}
		}

		// Pre-auth session is one-shot — burn it whether or not the next step
		// succeeds. _ on the error: best-effort cleanup; the worst case is a
		// dangling pre-auth row that expires on its own in PreAuthTTL.
		_ = sm.Revoke(r.Context(), sess.ID)

		remember := false
		if rc, err := r.Cookie("vac_pre_remember"); err == nil {
			remember, _ = strconv.ParseBool(rc.Value)
		}
		// Clear the carrier cookies now that we're upgrading.
		clearPreAuthCookie(w, r)
		http.SetCookie(w, &http.Cookie{
			Name:     "vac_pre_remember",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secureForRequest(r),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})

		issueFullSession(w, r, sm, cfg, user, remember)
	}
}
