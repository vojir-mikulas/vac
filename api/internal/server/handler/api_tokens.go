package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

const (
	maxAPITokenNameLen = 100
)

type createAPITokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

type createAPITokenResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type apiTokenDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// CreateAPIToken mints a new API token for the caller. The raw token value
// is returned in the response body and never persisted in plaintext — the
// user must store it now or never.
func CreateAPIToken(tm *auth.TokenManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var req createAPITokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			WriteError(w, http.StatusBadRequest, "name is required")
			return
		}
		if len(name) > maxAPITokenNameLen {
			WriteError(w, http.StatusBadRequest, "name too long")
			return
		}
		var expiresAt *time.Time
		if req.ExpiresInDays > 0 {
			t := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
			expiresAt = &t
		}

		raw, tok, err := tm.Create(r.Context(), u.ID, name, expiresAt)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not create token")
			return
		}
		WriteJSON(w, http.StatusCreated, createAPITokenResponse{
			ID:        tok.ID,
			Name:      tok.Name,
			Token:     raw,
			CreatedAt: tok.CreatedAt,
			ExpiresAt: tok.ExpiresAt,
		})
	}
}

// ListAPITokens returns the caller's tokens (id, name, timestamps). The raw
// token value is never returned — there is no API path to recover it.
func ListAPITokens(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		rows, err := s.ListAPITokensForUser(r.Context(), u.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list tokens")
			return
		}
		out := make([]apiTokenDTO, 0, len(rows))
		for _, t := range rows {
			out = append(out, apiTokenDTO{
				ID:         t.ID,
				Name:       t.Name,
				LastUsedAt: t.LastUsedAt,
				CreatedAt:  t.CreatedAt,
				ExpiresAt:  t.ExpiresAt,
			})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// RevokeAPIToken deletes a token by id. The store scopes the delete by
// user_id so users cannot revoke each other's tokens.
func RevokeAPIToken(tm *auth.TokenManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		if u == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := chi.URLParam(r, "id")
		if id == "" {
			WriteError(w, http.StatusBadRequest, "token id required")
			return
		}
		if err := tm.Revoke(r.Context(), u.ID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "token not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not revoke token")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"revoked": 1})
	}
}
