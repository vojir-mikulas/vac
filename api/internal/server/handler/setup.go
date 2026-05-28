package handler

import (
	"encoding/json"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const minPasswordLength = 8

type setupStatusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

type setupAdminRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type setupAdminResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// SetupStatus reports whether the dashboard needs to show the setup wizard
// (no users exist) or jump straight to the login page.
func SetupStatus(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := s.CountUsers(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "database error")
			return
		}
		WriteJSON(w, http.StatusOK, setupStatusResponse{NeedsSetup: n == 0})
	}
}

// SetupAdmin creates the first user. Refuses with 409 if any user exists —
// the wizard is single-use and cannot bootstrap a second admin.
func SetupAdmin(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setupAdminRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Username == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, "username and password are required")
			return
		}
		if len(req.Password) < minPasswordLength {
			WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
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

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not hash password")
			return
		}

		u, err := s.CreateUser(r.Context(), req.Username, hash)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not create user")
			return
		}

		WriteJSON(w, http.StatusCreated, setupAdminResponse{ID: u.ID, Username: u.Username})
	}
}
