package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type sshKeyDTO struct {
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

func toSSHKeyDTO(k store.SSHKey) sshKeyDTO {
	return sshKeyDTO{
		PublicKey:   k.PublicKey,
		Fingerprint: sshkey.Fingerprint(k.PublicKey),
		CreatedAt:   k.CreatedAt,
	}
}

// isSSHRepoURL is true for `git@host:path` or `ssh://...` URLs. HTTPS public
// repos don't need a deploy key — VAC skips key minting and returns 404 on
// GET so the UI can render the right empty state.
func isSSHRepoURL(url string) bool {
	url = strings.TrimSpace(url)
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	if strings.HasPrefix(url, "git@") && strings.Contains(url, ":") {
		return true
	}
	return false
}

// GetAppSSHKey returns the public deploy key for an app, minting one
// on-demand when the app uses an SSH-style git URL and no key exists yet.
func GetAppSSHKey(s *store.Store, mgr *sshkey.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}

		k, err := mgr.Get(r.Context(), app.ID)
		if errors.Is(err, store.ErrNotFound) {
			if !isSSHRepoURL(app.GitURL) {
				WriteError(w, http.StatusNotFound, "app uses an HTTPS git URL; no deploy key required")
				return
			}
			minted, mintErr := mgr.Mint(r.Context(), app)
			if mintErr != nil {
				if errors.Is(mintErr, sshkey.ErrEncryptionUnavailable) {
					WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; cannot mint deploy keys")
					return
				}
				WriteError(w, http.StatusInternalServerError, "could not mint ssh key")
				return
			}
			WriteJSON(w, http.StatusOK, toSSHKeyDTO(minted))
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load ssh key")
			return
		}
		WriteJSON(w, http.StatusOK, toSSHKeyDTO(k))
	}
}

// RegenerateAppSSHKey always issues a fresh key, replacing any existing one.
// Useful after a deploy-key leak or when the user rotates them on schedule.
func RegenerateAppSSHKey(s *store.Store, mgr *sshkey.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		minted, err := mgr.Mint(r.Context(), app)
		if err != nil {
			if errors.Is(err, sshkey.ErrEncryptionUnavailable) {
				WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; cannot mint deploy keys")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not mint ssh key")
			return
		}
		WriteJSON(w, http.StatusOK, toSSHKeyDTO(minted))
	}
}

// DeleteAppSSHKey removes the deploy key. Idempotent — already-absent is a
// 200 with deleted=0 rather than 404, so the UI can be reckless about the
// confirmation flow.
func DeleteAppSSHKey(mgr *sshkey.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := mgr.Delete(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteJSON(w, http.StatusOK, map[string]int{"deleted": 0})
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete ssh key")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"deleted": 1})
	}
}
