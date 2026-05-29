package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// DeploymentEnqueuer is the slice of deploy.Worker the handler depends on.
// Defined as an interface so handler tests can use a fake.
type DeploymentEnqueuer interface {
	Enqueue(deploymentID string) error
}

type deploymentDTO struct {
	ID            string     `json:"id"`
	AppID         string     `json:"app_id"`
	Status        string     `json:"status"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	ComposeHash   *string    `json:"compose_hash,omitempty"`
	CommitSHA     *string    `json:"commit_sha,omitempty"`
	CommitMessage *string    `json:"commit_message,omitempty"`
	Error         *string    `json:"error,omitempty"`
}

func toDeploymentDTO(d store.Deployment) deploymentDTO {
	return deploymentDTO{
		ID:            d.ID,
		AppID:         d.AppID,
		Status:        d.Status,
		TriggeredAt:   d.TriggeredAt,
		StartedAt:     d.StartedAt,
		FinishedAt:    d.FinishedAt,
		ComposeHash:   d.ComposeHash,
		CommitSHA:     d.CommitSHA,
		CommitMessage: d.CommitMessage,
		Error:         d.Error,
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
		d, err := s.CreateDeployment(r.Context(), app.ID)
		if err != nil {
			WriteError(rw, http.StatusInternalServerError, "could not create deployment")
			return
		}
		if err := w.Enqueue(d.ID); err != nil {
			if errors.Is(err, deploy.ErrQueueFull) {
				WriteError(rw, http.StatusServiceUnavailable, "deploy queue full — retry shortly")
				return
			}
			WriteError(rw, http.StatusInternalServerError, "could not enqueue deployment")
			return
		}
		WriteJSON(rw, http.StatusAccepted, toDeploymentDTO(d))
	}
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

// GetDeployment returns one deployment row by id.
func GetDeployment(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		WriteJSON(w, http.StatusOK, toDeploymentDTO(d))
	}
}
