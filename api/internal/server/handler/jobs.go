package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// JobRunner triggers a manual job run off the request path (plan:
// scheduled-jobs.md). Satisfied by *jobs.Engine.
type JobRunner interface {
	RunOnce(ctx context.Context, job store.ScheduledJob) error
}

type jobRunDTO struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Status     string     `json:"status"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Output     *string    `json:"output,omitempty"`
	Error      *string    `json:"error,omitempty"`
}

func toJobRunDTO(r store.JobRun) jobRunDTO {
	return jobRunDTO{
		ID:         r.ID,
		JobID:      r.JobID,
		StartedAt:  r.StartedAt,
		FinishedAt: r.FinishedAt,
		Status:     r.Status,
		ExitCode:   r.ExitCode,
		Output:     r.Output,
		Error:      r.Error,
	}
}

type scheduledJobDTO struct {
	ID              string     `json:"id"`
	AppID           string     `json:"app_id"`
	Name            string     `json:"name"`
	ServiceName     string     `json:"service_name"`
	Command         string     `json:"command"`
	Frequency       string     `json:"frequency"`
	IntervalMinutes *int       `json:"interval_minutes,omitempty"`
	HourOfDay       int        `json:"hour_of_day"`
	DayOfWeek       *int       `json:"day_of_week,omitempty"`
	TimeoutSeconds  int        `json:"timeout_seconds"`
	Enabled         bool       `json:"enabled"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastRun         *jobRunDTO `json:"last_run,omitempty"`
}

func toScheduledJobDTO(j store.ScheduledJob) scheduledJobDTO {
	return scheduledJobDTO{
		ID:              j.ID,
		AppID:           j.AppID,
		Name:            j.Name,
		ServiceName:     j.ServiceName,
		Command:         j.Command,
		Frequency:       j.Frequency,
		IntervalMinutes: j.IntervalMinutes,
		HourOfDay:       j.HourOfDay,
		DayOfWeek:       j.DayOfWeek,
		TimeoutSeconds:  j.TimeoutSeconds,
		Enabled:         j.Enabled,
		LastRunAt:       j.LastRun,
		NextRunAt:       j.NextRun,
		CreatedAt:       j.CreatedAt,
		UpdatedAt:       j.UpdatedAt,
	}
}

// jobReq is the create/update body.
type jobReq struct {
	Name            string `json:"name"`
	ServiceName     string `json:"service_name"`
	Command         string `json:"command"`
	Frequency       string `json:"frequency"`
	IntervalMinutes *int   `json:"interval_minutes"`
	HourOfDay       int    `json:"hour_of_day"`
	DayOfWeek       *int   `json:"day_of_week"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Enabled         *bool  `json:"enabled"`
}

// validate normalizes and checks the request, returning a client-facing message
// on failure.
func (req *jobReq) validate() string {
	if req.Name == "" {
		return "name is required"
	}
	if req.ServiceName == "" {
		return "service is required"
	}
	if req.Command == "" {
		return "command is required"
	}
	switch req.Frequency {
	case "interval":
		req.DayOfWeek = nil
		if req.IntervalMinutes == nil || *req.IntervalMinutes <= 0 {
			return "interval jobs require interval_minutes (a positive number)"
		}
	case "daily":
		req.IntervalMinutes = nil
		req.DayOfWeek = nil
	case "weekly":
		req.IntervalMinutes = nil
		if req.DayOfWeek == nil || *req.DayOfWeek < 0 || *req.DayOfWeek > 6 {
			return "weekly jobs require day_of_week (0-6, Sunday=0)"
		}
	default:
		return "frequency must be interval, daily, or weekly"
	}
	if req.Frequency != "interval" && (req.HourOfDay < 0 || req.HourOfDay > 23) {
		return "hour_of_day must be 0-23"
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 1800 // default 30-min hard cap
	}
	return ""
}

func (req *jobReq) toInput() store.ScheduledJobInput {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.ScheduledJobInput{
		Name:            req.Name,
		ServiceName:     req.ServiceName,
		Command:         req.Command,
		Frequency:       req.Frequency,
		IntervalMinutes: req.IntervalMinutes,
		HourOfDay:       req.HourOfDay,
		DayOfWeek:       req.DayOfWeek,
		TimeoutSeconds:  req.TimeoutSeconds,
		Enabled:         enabled,
	}
}

// ListJobs returns the scheduled jobs for an app, each with its most recent run
// inlined for the UI status pill.
func ListJobs(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		jobs, err := s.ListScheduledJobsForApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list jobs")
			return
		}
		out := make([]scheduledJobDTO, 0, len(jobs))
		for _, j := range jobs {
			dto := toScheduledJobDTO(j)
			if last, err := s.LatestJobRun(r.Context(), j.ID); err == nil {
				rd := toJobRunDTO(last)
				dto.LastRun = &rd
			}
			out = append(out, dto)
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// CreateJob adds a scheduled job. The service must exist on the app.
func CreateJob(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), appID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		var req jobReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if msg := req.validate(); msg != "" {
			WriteError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		if _, err := s.GetService(r.Context(), appID, req.ServiceName); err != nil {
			WriteError(w, http.StatusUnprocessableEntity, "no such service on this app")
			return
		}
		j, err := s.CreateScheduledJob(r.Context(), appID, req.toInput())
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "a job with this name already exists on this app")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create job")
			return
		}
		audit.SetTarget(r.Context(), "job", j.ID)
		audit.Describe(r.Context(), "created scheduled job "+app.Slug+"/"+req.Name)
		WriteJSON(w, http.StatusCreated, toScheduledJobDTO(j))
	}
}

// UpdateJob overwrites a job's mutable fields.
func UpdateJob(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		jid := chi.URLParam(r, "jid")
		existing, err := s.GetScheduledJob(r.Context(), jid)
		if err != nil || existing.AppID != appID {
			WriteError(w, http.StatusNotFound, "job not found")
			return
		}
		var req jobReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if msg := req.validate(); msg != "" {
			WriteError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		if _, err := s.GetService(r.Context(), appID, req.ServiceName); err != nil {
			WriteError(w, http.StatusUnprocessableEntity, "no such service on this app")
			return
		}
		j, err := s.UpdateScheduledJob(r.Context(), appID, jid, req.toInput())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "job not found")
				return
			}
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "a job with this name already exists on this app")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update job")
			return
		}
		audit.SetTarget(r.Context(), "job", j.ID)
		audit.Describe(r.Context(), "updated scheduled job "+j.Name)
		WriteJSON(w, http.StatusOK, toScheduledJobDTO(j))
	}
}

// DeleteJob removes a job (its runs cascade).
func DeleteJob(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		jid := chi.URLParam(r, "jid")
		if err := s.DeleteScheduledJob(r.Context(), appID, jid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "job not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete job")
			return
		}
		audit.SetTarget(r.Context(), "job", jid)
		audit.Describe(r.Context(), "deleted scheduled job")
		w.WriteHeader(http.StatusNoContent)
	}
}

// RunJob triggers a manual run. Returns 202 — the run executes detached so a
// long command doesn't block the request. The per-run timeout is enforced by the
// engine itself; the outer context just bounds the detached goroutine.
func RunJob(s *store.Store, runner JobRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		jid := chi.URLParam(r, "jid")
		job, err := s.GetScheduledJob(r.Context(), jid)
		if err != nil || job.AppID != appID {
			WriteError(w, http.StatusNotFound, "job not found")
			return
		}
		go func() {
			// One day is a generous outer bound; the engine's per-job
			// timeout_seconds is the real cap on the command itself.
			ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
			defer cancel()
			_ = runner.RunOnce(ctx, job)
		}()
		audit.SetTarget(r.Context(), "job", jid)
		audit.Describe(r.Context(), "triggered manual run of job "+job.Name)
		w.WriteHeader(http.StatusAccepted)
	}
}

// ListJobRuns returns a job's run history, newest first (incl. the output tail).
func ListJobRuns(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		jid := chi.URLParam(r, "jid")
		job, err := s.GetScheduledJob(r.Context(), jid)
		if err != nil || job.AppID != appID {
			WriteError(w, http.StatusNotFound, "job not found")
			return
		}
		runs, err := s.ListJobRuns(r.Context(), jid, 50)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list runs")
			return
		}
		out := make([]jobRunDTO, 0, len(runs))
		for _, run := range runs {
			out = append(out, toJobRunDTO(run))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}
