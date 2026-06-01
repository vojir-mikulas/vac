package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type setupStatusResponse struct {
	NeedsSetup    bool `json:"needs_setup"`
	TokenRequired bool `json:"token_required"`
}

type setupAdminRequest struct {
	Username   string `json:"username" validate:"required,min=1,max=64"`
	Password   string `json:"password" validate:"required,min=12,max=256"`
	SetupToken string `json:"setup_token" validate:"required"`
}

// SetupStatus reports whether the dashboard needs to show the setup wizard
// (no users exist) or jump straight to the login page. When setup is needed,
// also reports whether the operator must supply the first-boot token — true
// in the normal case where vac-api generated one at boot.
func SetupStatus(s *store.Store, workDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := s.CountUsers(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "database error")
			return
		}
		resp := setupStatusResponse{NeedsSetup: n == 0}
		if resp.NeedsSetup {
			tok, _ := auth.ReadSetupToken(workDir)
			resp.TokenRequired = tok != ""
		}
		WriteJSON(w, http.StatusOK, resp)
	}
}

// SetupAdmin creates the first user, issues a session, and returns the new
// user — the wizard lands the operator directly on the dashboard rather than
// kicking them back through the login form. Refuses with 409 if any user
// exists; the wizard is single-use and cannot bootstrap a second admin.
//
// Gated by the first-boot setup token written to ${WorkDir}/setup.token,
// which proves whoever is calling has host filesystem access. Without this,
// a public-facing fresh install is a land-grab race for whichever HTTP client
// reaches it first.
func SetupAdmin(s *store.Store, sm *auth.SessionManager, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setupAdminRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if msg, ok := validateStruct(req); !ok {
			WriteError(w, http.StatusBadRequest, msg)
			return
		}

		n, err := s.CountUsers(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "database error")
			return
		}
		if n > 0 {
			WriteError(w, http.StatusConflict, "setup already complete")
			return
		}

		if err := auth.ConsumeSetupToken(cfg.WorkDir, req.SetupToken); err != nil {
			switch {
			case errors.Is(err, auth.ErrSetupTokenMissing):
				WriteError(w, http.StatusServiceUnavailable, "no setup token on disk — restart vac-api to regenerate one")
			case errors.Is(err, auth.ErrSetupTokenMismatch):
				WriteError(w, http.StatusUnauthorized, "invalid setup token")
			default:
				WriteError(w, http.StatusInternalServerError, "could not validate setup token")
			}
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			// Token already consumed; regenerate so the operator can retry
			// without restarting vac-api. Best-effort.
			_, _ = auth.EnsureSetupToken(cfg.WorkDir)
			WriteError(w, http.StatusInternalServerError, "could not hash password")
			return
		}

		u, err := s.CreateUser(r.Context(), req.Username, hash)
		if err != nil {
			_, _ = auth.EnsureSetupToken(cfg.WorkDir)
			WriteError(w, http.StatusInternalServerError, "could not create user")
			return
		}

		issueFullSession(w, r, sm, u, true)
	}
}
