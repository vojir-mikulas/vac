package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// DeploymentEnqueuer is the slice of deploy.Worker the handler depends on.
// Defined as an interface so handler tests can use a fake.
type DeploymentEnqueuer interface {
	Enqueue(deploymentID string) error
}

// DeploymentCanceller is the slice of deploy.Worker the cancel handler needs.
// Cancel interrupts an in-flight deploy (returns false if it was only queued);
// NotifyChanged refreshes the live deploy-queue panel after a queued cancel.
type DeploymentCanceller interface {
	Cancel(deploymentID string) bool
	NotifyChanged()
}

// detachedCtx returns a short-lived context independent of the request, for
// best-effort cleanup (settling a stranded `queued` row after a failed Enqueue)
// that must run even if the client disconnected at that instant.
func detachedCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

type deploymentDTO struct {
	ID             string     `json:"id"`
	AppID          string     `json:"app_id"`
	Status         string     `json:"status"`
	TriggeredAt    time.Time  `json:"triggered_at"`
	TriggeredBy    string     `json:"triggered_by"`
	RolledBackFrom *string    `json:"rolled_back_from,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	ComposeHash    *string    `json:"compose_hash,omitempty"`
	CommitSHA      *string    `json:"commit_sha,omitempty"`
	CommitMessage  *string    `json:"commit_message,omitempty"`
	Error          *string    `json:"error,omitempty"`
}

func toDeploymentDTO(d store.Deployment) deploymentDTO {
	return deploymentDTO{
		ID:             d.ID,
		AppID:          d.AppID,
		Status:         d.Status,
		TriggeredAt:    d.TriggeredAt,
		TriggeredBy:    d.TriggeredBy,
		RolledBackFrom: d.RolledBackFrom,
		StartedAt:      d.StartedAt,
		FinishedAt:     d.FinishedAt,
		ComposeHash:    d.ComposeHash,
		CommitSHA:      d.CommitSHA,
		CommitMessage:  d.CommitMessage,
		Error:          d.Error,
	}
}

// TriggerDeployment creates a row and enqueues it for the worker. Returns
// 202 with the new deployment so the UI can immediately poll its status.
// 503 if the queue is at capacity — retry-able from the caller's side.
func TriggerDeployment(s *store.Store, w DeploymentEnqueuer) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(rw, http.StatusNotFound, "app not found")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not load app")
			return
		}
		d, err := s.CreateDeployment(r.Context(), app.ID, store.TriggeredManual, nil)
		if err != nil {
			if errors.Is(err, store.ErrActiveDeploymentExists) {
				WriteError(rw, http.StatusConflict, "a deploy for this app is already in progress")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not create deployment")
			return
		}
		if err := w.Enqueue(d.ID); err != nil {
			// The row is already `queued`; settle it so a transient queue-full doesn't
			// strand the app's only deploy lane until the reaper settles it (~30 min).
			cctx, cancel := detachedCtx()
			_ = s.FailQueuedDeployment(cctx, d.ID, "deploy queue full at enqueue")
			cancel()
			if errors.Is(err, deploy.ErrQueueFull) {
				WriteError(rw, http.StatusServiceUnavailable, "deploy queue full — retry shortly")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not enqueue deployment")
			return
		}
		// Audit hook — the central middleware records actor/route/outcome; this
		// one line gives the entry its target and a human summary.
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Action(r.Context(), "deployment.triggered", map[string]any{"app": app.Slug})
		WriteJSON(rw, http.StatusAccepted, toDeploymentDTO(d))
	}
}

// RollbackDeployment re-deploys the commit of a prior successful deployment
// (`did`), recording it as a new deployment with triggered_by=rollback and
// rolled_back_from pointing at the source. Code only — env vars are not rolled
// back. 202 with the new deployment; 404 unknown source; 422 invalid source
// (wrong app / not a successful deploy); 503 if the queue is full.
func RollbackDeployment(s *store.Store, w DeploymentEnqueuer) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		sourceID := chi.URLParam(r, "did")
		app, err := s.GetApp(r.Context(), appID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(rw, http.StatusNotFound, "app not found")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not load app")
			return
		}
		d, err := s.CreateRollbackDeployment(r.Context(), app.ID, sourceID)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				WriteError(rw, http.StatusNotFound, "deployment not found")
			case errors.Is(err, store.ErrRollbackSourceInvalid):
				WriteError(rw, http.StatusUnprocessableEntity, "can only roll back to a successful deployment of this app")
			case errors.Is(err, store.ErrActiveDeploymentExists):
				WriteError(rw, http.StatusConflict, "a deploy for this app is already in progress")
			default:
				WriteError(rw, http.StatusInternalServerError, "could not create rollback")
			}
			return
		}
		if err := w.Enqueue(d.ID); err != nil {
			// Settle the just-queued rollback row so a transient queue-full doesn't
			// strand the app's deploy lane (see TriggerDeployment).
			cctx, cancel := detachedCtx()
			_ = s.FailQueuedDeployment(cctx, d.ID, "deploy queue full at enqueue")
			cancel()
			if errors.Is(err, deploy.ErrQueueFull) {
				WriteError(rw, http.StatusServiceUnavailable, "deploy queue full — retry shortly")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not enqueue rollback")
			return
		}
		shortSHA := "previous version"
		if d.CommitSHA != nil && len(*d.CommitSHA) >= 7 {
			shortSHA = (*d.CommitSHA)[:7]
		}
		audit.SetTarget(r.Context(), "app", app.ID)
		audit.Action(r.Context(), "deployment.rolled_back", map[string]any{"app": app.Slug, "sha": shortSHA})
		WriteJSON(rw, http.StatusAccepted, toDeploymentDTO(d))
	}
}

// CancelDeployment stops a queued or in-flight deployment and settles it as
// `canceled`. For an in-flight deploy, Cancel aborts the running git/docker
// subprocess (its deploy context dies, so the pipeline's own DB writes no-op);
// we then record the terminal status on the request context, which is alive. A
// still-queued deploy isn't in the pool — we settle the row directly, and the
// worker skips it when it later dequeues (the pipeline's already-settled guard).
//
// The prior stack keeps running — cancelling a deploy never tears down what's
// already serving (the same invariant as a failed deploy). 404 unknown; 422 if
// the deployment has already settled.
//
// POST /api/apps/{id}/deployments/{did}/cancel
func CancelDeployment(s *store.Store, c DeploymentCanceller) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		did := chi.URLParam(r, "did")
		d, err := s.GetDeployment(r.Context(), did)
		if err != nil || d.AppID != appID {
			WriteError(rw, http.StatusNotFound, "deployment not found")
			return
		}
		if deploy.IsTerminalDeploymentStatus(d.Status) {
			WriteError(rw, http.StatusUnprocessableEntity, "deployment has already finished")
			return
		}

		c.Cancel(did) // aborts the subprocess if in-flight; no-op if only queued

		msg := "canceled by user"
		if err := s.MarkDeploymentFinished(r.Context(), did, deploy.DeploymentStatusCanceled, &msg); err != nil {
			WriteError(rw, http.StatusInternalServerError, "could not cancel deployment")
			return
		}
		// The build/up was interrupted, so the app's services no longer reflect a
		// deploy in progress — recompute the stack status from what's actually
		// there (no services yet → "created"; prior stack still up → "running").
		recomputeAppStatus(r.Context(), s, appID)
		c.NotifyChanged()

		audit.SetTarget(r.Context(), "app", appID)
		audit.Action(r.Context(), "deployment.canceled", nil)
		WriteJSON(rw, http.StatusOK, map[string]string{"status": deploy.DeploymentStatusCanceled})
	}
}

// recomputeAppStatus derives the stack status from the app's current persisted
// service statuses and writes it. Used after a cancel, when the pipeline's own
// status writes were interrupted.
func recomputeAppStatus(ctx context.Context, s *store.Store, appID string) {
	rows, err := s.ListServicesForApp(ctx, appID)
	if err != nil {
		return
	}
	statuses := make([]string, 0, len(rows))
	for _, r := range rows {
		statuses = append(statuses, r.Status)
	}
	_ = s.SetAppStatus(ctx, appID, deploy.DeriveAppStatus(statuses))
}

// ListDeployments returns the most recent deployments for the app, newest
// first. Cap of 100 is enforced at the store layer.
func ListDeployments(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rows, err := s.ListDeploymentsForApp(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list deployments")
			return
		}
		out := make([]deploymentDTO, 0, len(rows))
		for _, d := range rows {
			out = append(out, toDeploymentDTO(d))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// GetDeployment returns one deployment row by id, scoped to the app in the URL
// so a deployment id can't be read out from under a different app's path.
func GetDeployment(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		did := chi.URLParam(r, "did")
		d, err := s.GetDeployment(r.Context(), did)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "deployment not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load deployment")
			return
		}
		if d.AppID != appID {
			WriteError(w, http.StatusNotFound, "deployment not found")
			return
		}
		WriteJSON(w, http.StatusOK, toDeploymentDTO(d))
	}
}
