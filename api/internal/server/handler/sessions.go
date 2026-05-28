package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type sessionDTO struct {
	ID         string    `json:"id"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IsCurrent  bool      `json:"is_current"`
}

type revokeResponse struct {
	Revoked int64 `json:"revoked"`
}

// ListSessions returns the caller's active sessions, marking the one the
// request rode in on as current. Mounted behind RequireSession.
func ListSessions(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		current := auth.Session(r.Context())
		if u == nil || current == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		rows, err := s.ListSessionsForUser(r.Context(), u.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list sessions")
			return
		}
		out := make([]sessionDTO, 0, len(rows))
		for _, sess := range rows {
			out = append(out, sessionDTO{
				ID:         sess.ID,
				IP:         ipString(sess),
				UserAgent:  sess.UserAgent,
				CreatedAt:  sess.CreatedAt,
				LastSeenAt: sess.LastSeenAt,
				ExpiresAt:  sess.ExpiresAt,
				IsCurrent:  sess.ID == current.ID,
			})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// RevokeSession deletes a single session by id. The current session cannot
// be deleted here — callers should use /api/auth/logout for that, which also
// clears the cookies.
func RevokeSession(s *store.Store, sm *auth.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		current := auth.Session(r.Context())
		if u == nil || current == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := chi.URLParam(r, "id")
		if id == "" {
			WriteError(w, http.StatusBadRequest, "session id required")
			return
		}
		if id == current.ID {
			WriteError(w, http.StatusConflict, "use /api/auth/logout to revoke the current session")
			return
		}
		// Confirm the session belongs to this user before deleting — the
		// store layer doesn't enforce ownership, so the handler must.
		rows, err := s.ListSessionsForUser(r.Context(), u.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load sessions")
			return
		}
		var found bool
		for _, sess := range rows {
			if sess.ID == id {
				found = true
				break
			}
		}
		if !found {
			WriteError(w, http.StatusNotFound, "session not found")
			return
		}
		if err := sm.Revoke(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "session not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not revoke session")
			return
		}
		WriteJSON(w, http.StatusOK, revokeResponse{Revoked: 1})
	}
}

// RevokeOtherSessions deletes every session for the caller except the one
// they're using right now — the "sign out everywhere else" button.
func RevokeOtherSessions(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.User(r.Context())
		current := auth.Session(r.Context())
		if u == nil || current == nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		n, err := s.RevokeOtherSessionsForUser(r.Context(), u.ID, current.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not revoke sessions")
			return
		}
		WriteJSON(w, http.StatusOK, revokeResponse{Revoked: n})
	}
}

func ipString(sess store.Session) string {
	if sess.IPAddress == nil {
		return ""
	}
	return sess.IPAddress.String()
}
