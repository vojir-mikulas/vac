package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// guestAccessMinLen is the shortest shared access code we accept. The redeem
// endpoint is rate-limited, but a too-short code is still trivially guessable —
// nudge the operator toward something with a little entropy.
const guestAccessMinLen = 6

type guestAccessDTO struct {
	// Enabled reports whether a shared access code is set for the service (which
	// is what lets non-operators past the gate). The code itself is only ever
	// returned by the reveal endpoint, never here.
	Enabled bool `json:"enabled"`
}

// GetGuestAccess reports whether the service has a shared access code set.
func GetGuestAccess(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID, name := chi.URLParam(r, "id"), chi.URLParam(r, "name")
		enc, err := s.GetServiceGuestAccessCode(r.Context(), appID, name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load guest access")
			return
		}
		WriteJSON(w, http.StatusOK, guestAccessDTO{Enabled: len(enc) > 0})
	}
}

type setGuestAccessRequest struct {
	Code string `json:"code"`
}

// SetGuestAccess sets (or rotates) the service's shared access code. It is sealed
// at rest; rotating it invalidates the previous code for everyone immediately.
func SetGuestAccess(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID, name := chi.URLParam(r, "id"), chi.URLParam(r, "name")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; the access code cannot be encrypted")
			return
		}
		var req setGuestAccessRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		code := strings.TrimSpace(req.Code)
		if len(code) < guestAccessMinLen {
			WriteError(w, http.StatusBadRequest, "access code must be at least 6 characters")
			return
		}
		sealed, err := box.Seal([]byte(code))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not encrypt access code")
			return
		}
		if err := s.SetServiceGuestAccessCode(r.Context(), appID, name, sealed); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not save access code")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "set shared access code for "+name)
		WriteJSON(w, http.StatusOK, guestAccessDTO{Enabled: true})
	}
}

// DeleteGuestAccess clears the service's shared access code, so only operators
// pass the login gate for it again.
func DeleteGuestAccess(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID, name := chi.URLParam(r, "id"), chi.URLParam(r, "name")
		if err := s.SetServiceGuestAccessCode(r.Context(), appID, name, nil); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not clear access code")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "removed shared access code for "+name)
		WriteJSON(w, http.StatusOK, map[string]int{"cleared": 1})
	}
}

type revealGuestAccessDTO struct {
	Code string `json:"code"`
}

// RevealGuestAccess returns the plaintext shared access code so the operator can
// re-share it. Behind the session like the env-var reveal; 404s when none set.
func RevealGuestAccess(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID, name := chi.URLParam(r, "id"), chi.URLParam(r, "name")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured")
			return
		}
		enc, err := s.GetServiceGuestAccessCode(r.Context(), appID, name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "service not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load access code")
			return
		}
		if len(enc) == 0 {
			WriteError(w, http.StatusNotFound, "no access code set")
			return
		}
		code, err := box.Open(enc)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not decrypt access code")
			return
		}
		WriteJSON(w, http.StatusOK, revealGuestAccessDTO{Code: string(code)})
	}
}
