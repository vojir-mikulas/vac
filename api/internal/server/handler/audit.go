package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/revert"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// auditLogDTO is the read shape for the activity feed. The before-snapshot in
// metadata is deliberately NOT exposed — it can hold sealed secrets and is only
// the engine's business — so the DTO carries just the displayable fields.
type auditLogDTO struct {
	ID         string     `json:"id"`
	ActorType  string     `json:"actor_type"`
	Actor      string     `json:"actor"` // resolved username, or "" for system/anonymous
	Action     string     `json:"action"`
	TargetType *string    `json:"target_type,omitempty"`
	TargetID   *string    `json:"target_id,omitempty"`
	Summary    *string    `json:"summary,omitempty"`
	StatusCode int        `json:"status_code"`
	Revertable bool       `json:"revertable"`
	RevertedAt *time.Time `json:"reverted_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListAudit returns the recent audit log, newest first — the durable activity
// feed that supersedes the deployment-derived one (plan 11, Part 1). Optional
// ?limit=N (clamped by the store).
//
// GET /api/audit
func ListAudit(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil {
				limit = n
			}
		}
		rows, err := s.ListAuditLog(r.Context(), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list activity")
			return
		}
		names := resolveActorNames(r.Context(), s, rows)
		out := make([]auditLogDTO, 0, len(rows))
		for _, a := range rows {
			out = append(out, toAuditDTO(a, names))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// RevertAudit undoes a revertable audit entry by reapplying its before-snapshot
// (plan 11, Part 2). The revert is itself an audited action (the middleware
// records this POST). 422 for a non-revertable entry, 409 if already reverted.
//
// POST /api/audit/{id}/revert
func RevertAudit(rv *revert.Reverter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		summary, err := rv.Revert(r.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				WriteError(w, http.StatusNotFound, "activity entry not found")
			case errors.Is(err, store.ErrConflict):
				WriteError(w, http.StatusConflict, "already reverted")
			case errors.Is(err, revert.ErrNotRevertable):
				WriteErrorCode(w, http.StatusUnprocessableEntity, "not_revertable", "this action cannot be reverted")
			default:
				WriteError(w, http.StatusInternalServerError, "could not revert")
			}
			return
		}
		// Attribute the revert to the entry it undid, with a clear summary.
		audit.SetTarget(r.Context(), "audit_log", id)
		audit.Describe(r.Context(), "reverted: "+summary)
		WriteJSON(w, http.StatusOK, map[string]string{"reverted": id, "summary": summary})
	}
}

func toAuditDTO(a store.AuditLog, names map[string]string) auditLogDTO {
	dto := auditLogDTO{
		ID:         a.ID,
		ActorType:  a.ActorType,
		Action:     a.Action,
		TargetType: a.TargetType,
		TargetID:   a.TargetID,
		Summary:    a.Summary,
		StatusCode: a.StatusCode,
		Revertable: a.Revertable && a.RevertedAt == nil,
		RevertedAt: a.RevertedAt,
		CreatedAt:  a.CreatedAt,
	}
	if a.ActorUserID != nil {
		dto.Actor = names[*a.ActorUserID]
	}
	return dto
}

// resolveActorNames builds a userID→username map for the entries' actors. One
// lookup per distinct user (single-operator today, so this is a handful), cached
// so repeated actors don't re-query. A missing user resolves to "".
func resolveActorNames(ctx context.Context, s *store.Store, rows []store.AuditLog) map[string]string {
	names := map[string]string{}
	for _, a := range rows {
		if a.ActorUserID == nil {
			continue
		}
		uid := *a.ActorUserID
		if _, done := names[uid]; done {
			continue
		}
		names[uid] = ""
		if u, err := s.GetUserByID(ctx, uid); err == nil {
			names[uid] = u.Username
		}
	}
	return names
}
