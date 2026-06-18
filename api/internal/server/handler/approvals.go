package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Approval gate (maintenance-mode-and-deploy-gates.md, Phase 4). A push matching
// an approval-gated trigger creates a `pending-approval` deployment that is not
// enqueued until an operator approves it; reject settles it terminally.

// ListPendingDeployments returns an app's deploys awaiting approval, oldest
// first.
func ListPendingDeployments(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rows, err := s.ListPendingApprovals(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list pending approvals")
			return
		}
		out := make([]deploymentDTO, 0, len(rows))
		for _, d := range rows {
			out = append(out, toDeploymentDTO(d))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// ApproveDeployment releases a pending deploy to the worker. It flips the row to
// `queued` (scoped to the app + pending status, so it can't release another
// app's deploy) and enqueues it.
func ApproveDeployment(s *store.Store, worker DeploymentEnqueuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		did := chi.URLParam(r, "did")
		d, err := s.ApproveDeployment(r.Context(), appID, did)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "no pending deployment found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not approve deployment")
			return
		}
		if err := worker.Enqueue(d.ID); err != nil {
			// The deploy was flipped to `queued`; revert it to `pending-approval` so a
			// transient queue-full doesn't strand the app's deploy lane and the
			// operator can simply re-approve.
			cctx, cancel := detachedCtx()
			_ = s.UnqueueApproval(cctx, appID, d.ID)
			cancel()
			if errors.Is(err, deploy.ErrQueueFull) {
				WriteError(w, http.StatusServiceUnavailable, "deploy queue full — retry shortly")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not enqueue deployment")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "approved a pending deployment")
		WriteJSON(w, http.StatusOK, toDeploymentDTO(d))
	}
}

// RejectDeployment settles a pending deploy terminally (canceled).
func RejectDeployment(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		did := chi.URLParam(r, "did")
		d, err := s.RejectDeployment(r.Context(), appID, did)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "no pending deployment found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not reject deployment")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Describe(r.Context(), "rejected a pending deployment")
		WriteJSON(w, http.StatusOK, toDeploymentDTO(d))
	}
}
