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

const maxRegistryFieldLen = 500

// registryAuthConfigDTO is the read shape: whether creds are stored and, when
// they are, the (non-secret) registry host so the edit form can prefill it. The
// username and password are never returned after they're written.
type registryAuthConfigDTO struct {
	Configured bool   `json:"configured"`
	Registry   string `json:"registry,omitempty"`
}

type setRegistryAuthRequest struct {
	Registry string `json:"registry,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// GetAppRegistryAuth reports whether private-registry credentials are stored for
// the app and, if so, the registry host. The password is never returned — the
// store only holds sealed ciphertext, and this endpoint opens it solely to echo
// back the non-secret host (mirrors the env-var sensitive-value pattern, D9).
func GetAppRegistryAuth(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		enc, err := s.GetAppRegistryAuth(r.Context(), appID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load registry auth")
			return
		}
		dto := registryAuthConfigDTO{Configured: len(enc) > 0}
		// Echo the registry host back so the edit form can prefill it. Best-effort:
		// without the key we just report "configured" with no host.
		if len(enc) > 0 && box != nil {
			if plain, oerr := box.Open(enc); oerr == nil {
				var creds store.RegistryAuth
				if json.Unmarshal(plain, &creds) == nil {
					dto.Registry = creds.Registry
				}
			}
		}
		WriteJSON(w, http.StatusOK, dto)
	}
}

// SetAppRegistryAuth seals {registry, username, password} and stores it on the
// app. Requires VAC_MASTER_KEY (same posture as TOTP / webhook URLs, D8) — a
// missing key returns 503 and only public images can deploy. The password is
// write-only: it is never echoed back.
func SetAppRegistryAuth(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; registry credentials cannot be encrypted")
			return
		}
		var req setRegistryAuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		req.Registry = strings.TrimSpace(req.Registry)
		req.Username = strings.TrimSpace(req.Username)
		// Trim the password too so a whitespace-only value is rejected rather than
		// sealed and then failing `docker login` opaquely. Registry tokens never
		// carry surrounding whitespace, so this can't truncate a real credential.
		req.Password = strings.TrimSpace(req.Password)
		if req.Username == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, "username and password are required")
			return
		}
		if len(req.Registry) > maxRegistryFieldLen || len(req.Username) > maxRegistryFieldLen || len(req.Password) > maxRegistryFieldLen {
			WriteError(w, http.StatusBadRequest, "registry, username and password must each be at most 500 chars")
			return
		}
		plain, err := json.Marshal(store.RegistryAuth{
			Registry: req.Registry,
			Username: req.Username,
			Password: req.Password,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not encode registry auth")
			return
		}
		sealed, err := box.Seal(plain)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not encrypt registry auth")
			return
		}
		if err := s.SetAppRegistryAuth(r.Context(), appID, sealed); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not store registry auth")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "set private-registry credentials")
		WriteJSON(w, http.StatusOK, registryAuthConfigDTO{Configured: true, Registry: req.Registry})
	}
}

// DeleteAppRegistryAuth clears stored registry credentials (revert to a public
// pull). Idempotent — clearing when none are set is a no-op success.
func DeleteAppRegistryAuth(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if err := s.SetAppRegistryAuth(r.Context(), appID, nil); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not clear registry auth")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "cleared private-registry credentials")
		WriteJSON(w, http.StatusOK, registryAuthConfigDTO{Configured: false})
	}
}
