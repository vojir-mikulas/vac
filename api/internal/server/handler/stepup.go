package handler

import (
	"encoding/json"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/auth"
)

type stepUpRequest struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
	Password     string `json:"password"`
}

// StepUp re-proves the current session's identity for a sensitive action. On
// success it stamps the session so RequireStepUp lets destructive routes through
// for auth.StepUpTTL. Unlike TOTPLogin this runs on a *full* session — the user
// is already logged in; this only refreshes their step-up freshness.
//
// The factor demanded depends on the account's posture:
//   - TOTP enabled  → a fresh authenticator code or a recovery code. A password
//     is NOT accepted here: it must not be possible to downgrade past the second
//     factor.
//   - TOTP disabled → a password re-entry. Without this, RequireStepUp would have
//     nothing to verify and would have to wave destructive ops through on the
//     session cookie alone — defeating the step-up gate exactly when there is no
//     second factor as a backstop.
//
// Mounted behind RequireSession.
func StepUp(sm *auth.SessionManager, tm *auth.TOTPManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		sess := auth.Session(r.Context())
		if u == nil || sess == nil {
			// No cookie session (e.g. API-token auth). Step-up is a browser-flow
			// concept keyed on the session row; there is nothing to stamp.
			WriteError(w, http.StatusBadRequest, "step-up requires an interactive session")
			return
		}

		var req stepUpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}

		if !u.TOTPEnabled {
			// Password re-entry is the step-up factor when there is no TOTP.
			if req.Password == "" {
				WriteError(w, http.StatusBadRequest, "password is required")
				return
			}
			if err := auth.VerifyPassword(u.PasswordHash, req.Password); err != nil {
				auditAuthFailure(r, "bad_stepup_password", u.Username, u.ID)
				WriteError(w, http.StatusUnauthorized, "invalid password")
				return
			}
		} else {
			if req.Code == "" && req.RecoveryCode == "" {
				WriteError(w, http.StatusBadRequest, "code or recovery_code is required")
				return
			}
			if req.Code != "" {
				if err := tm.Verify(r.Context(), u.ID, req.Code); err != nil {
					auditAuthFailure(r, "bad_stepup_totp", u.Username, u.ID)
					WriteError(w, http.StatusUnauthorized, "invalid code")
					return
				}
			} else {
				if err := tm.ConsumeRecoveryCode(r.Context(), u.ID, req.RecoveryCode); err != nil {
					auditAuthFailure(r, "bad_stepup_recovery_code", u.Username, u.ID)
					WriteError(w, http.StatusUnauthorized, "invalid recovery code")
					return
				}
			}
		}

		if err := sm.MarkStepUp(r.Context(), sess.ID); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not record step-up")
			return
		}

		audit.Action(r.Context(), "stepup.verified", nil)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "verified"})
	}
}
