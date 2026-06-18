package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/backup"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// BackupRunner triggers a manual backup off the request path (Track D / D1).
// Satisfied by *backup.Engine.
type BackupRunner interface {
	RunOnce(ctx context.Context, cfg store.BackupConfig) error
}

// BackupRestorer replays a recorded run back into its target container off the
// request path. Satisfied by *backup.Restorer.
type BackupRestorer interface {
	Restore(ctx context.Context, cfg store.BackupConfig, sourceRunID string) error
	CanRestore(cfg store.BackupConfig) bool
}

// BackupVerifier runs a non-destructive restorability check off the request
// path. Satisfied by *backup.Verifier.
type BackupVerifier interface {
	VerifyOnce(ctx context.Context, cfg store.BackupConfig) error
	CanVerify(cfg store.BackupConfig) bool
}

type backupConfigDTO struct {
	ID          string        `json:"id"`
	AppID       string        `json:"app_id"`
	ServiceName string        `json:"service_name"`
	Command     string        `json:"command"`
	Frequency   string        `json:"frequency"`
	HourOfDay   int           `json:"hour_of_day"`
	DayOfWeek   *int          `json:"day_of_week,omitempty"`
	Destination string        `json:"destination"`
	KeepCount   int           `json:"keep_count"`
	Enabled     bool          `json:"enabled"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastRun     *backupRunDTO `json:"last_run,omitempty"`
	// Restorable is true when this config's command maps to a known restore
	// command, so the UI can offer Restore (false → custom command, download +
	// restore manually). Set by the handlers that hold a restorer.
	Restorable bool `json:"restorable"`
	// Verifiable mirrors Restorable for the restorability check; LastVerification
	// is the most recent check's outcome (nil if never run). Set by handlers that
	// hold a verifier.
	Verifiable       bool             `json:"verifiable"`
	LastVerification *verificationDTO `json:"last_verification,omitempty"`
}

// verificationDTO is one restorability-check attempt for the UI badge/history.
type verificationDTO struct {
	ID          string     `json:"id"`
	ConfigID    string     `json:"config_id"`
	SourceRunID string     `json:"source_run_id"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Status      string     `json:"status"`
	Error       *string    `json:"error,omitempty"`
}

func toVerificationDTO(v store.BackupVerification) verificationDTO {
	return verificationDTO{
		ID:          v.ID,
		ConfigID:    v.ConfigID,
		SourceRunID: v.SourceRunID,
		StartedAt:   v.StartedAt,
		FinishedAt:  v.FinishedAt,
		Status:      v.Status,
		Error:       v.Error,
	}
}

// restoreRunDTO is one restore attempt for the progress view.
type restoreRunDTO struct {
	ID          string     `json:"id"`
	ConfigID    string     `json:"config_id"`
	SourceRunID string     `json:"source_run_id"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Status      string     `json:"status"`
	Error       *string    `json:"error,omitempty"`
}

func toRestoreRunDTO(r store.BackupRestore) restoreRunDTO {
	return restoreRunDTO{
		ID:          r.ID,
		ConfigID:    r.ConfigID,
		SourceRunID: r.SourceRunID,
		StartedAt:   r.StartedAt,
		FinishedAt:  r.FinishedAt,
		Status:      r.Status,
		Error:       r.Error,
	}
}

type backupRunDTO struct {
	ID          string     `json:"id"`
	ConfigID    string     `json:"config_id"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Status      string     `json:"status"`
	SizeBytes   *int64     `json:"size_bytes,omitempty"`
	ArtifactKey *string    `json:"artifact_key,omitempty"`
	Error       *string    `json:"error,omitempty"`
}

func toBackupConfigDTO(c store.BackupConfig) backupConfigDTO {
	return backupConfigDTO{
		ID:          c.ID,
		AppID:       c.AppID,
		ServiceName: c.ServiceName,
		Command:     c.Command,
		Frequency:   c.Frequency,
		HourOfDay:   c.HourOfDay,
		DayOfWeek:   c.DayOfWeek,
		Destination: c.Destination,
		KeepCount:   c.KeepCount,
		Enabled:     c.Enabled,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func toBackupRunDTO(r store.BackupRun) backupRunDTO {
	return backupRunDTO{
		ID:          r.ID,
		ConfigID:    r.ConfigID,
		StartedAt:   r.StartedAt,
		FinishedAt:  r.FinishedAt,
		Status:      r.Status,
		SizeBytes:   r.SizeBytes,
		ArtifactKey: r.ArtifactKey,
		Error:       r.Error,
	}
}

// backupConfigReq is the create/update body. S3 credentials are only present on
// the way in; they're sealed and never returned.
type backupConfigReq struct {
	ServiceName string           `json:"service_name"`
	Command     string           `json:"command"`
	Frequency   string           `json:"frequency"`
	HourOfDay   int              `json:"hour_of_day"`
	DayOfWeek   *int             `json:"day_of_week"`
	Destination string           `json:"destination"`
	S3          *backup.S3Config `json:"s3"`
	KeepCount   int              `json:"keep_count"`
	Enabled     *bool            `json:"enabled"`
}

// validate normalizes and checks the request, returning a client-facing message
// on failure.
func (req *backupConfigReq) validate() string {
	if req.Command == "" {
		return "command is required"
	}
	switch req.Frequency {
	case "daily":
		req.DayOfWeek = nil
	case "weekly":
		if req.DayOfWeek == nil || *req.DayOfWeek < 0 || *req.DayOfWeek > 6 {
			return "weekly backups require day_of_week (0-6, Sunday=0)"
		}
	default:
		return "frequency must be daily or weekly"
	}
	if req.HourOfDay < 0 || req.HourOfDay > 23 {
		return "hour_of_day must be 0-23"
	}
	switch req.Destination {
	case "local":
	case "s3":
		if req.S3 == nil {
			return "s3 destination requires s3 credentials"
		}
	default:
		return "destination must be local or s3"
	}
	if req.KeepCount <= 0 {
		req.KeepCount = 7
	}
	return ""
}

// ListBackups returns the per-service backup configs for an app, each with its
// most recent run inlined for the UI status pill and a `restorable` flag so the
// UI knows whether to offer Restore. restorer may be nil (restore disabled).
func ListBackups(s *store.Store, restorer BackupRestorer, verifier BackupVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		configs, err := s.ListBackupConfigsForApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list backups")
			return
		}
		out := make([]backupConfigDTO, 0, len(configs))
		for _, c := range configs {
			dto := toBackupConfigDTO(c)
			if last, err := s.LatestBackupRun(r.Context(), c.ID); err == nil {
				rd := toBackupRunDTO(last)
				dto.LastRun = &rd
			}
			dto.Restorable = restorer != nil && restorer.CanRestore(c)
			dto.Verifiable = verifier != nil && verifier.CanVerify(c)
			if dto.Verifiable {
				if lv, err := s.LatestVerification(r.Context(), c.ID); err == nil {
					vd := toVerificationDTO(lv)
					dto.LastVerification = &vd
				}
			}
			out = append(out, dto)
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// VerifyBackup triggers a non-destructive restorability check for a config's
// latest successful backup, off the request path (202, detached with a 30-min
// timeout like RunBackup). Refuses a custom-command config (422) and a
// concurrent verification (409) up front.
func VerifyBackup(s *store.Store, verifier BackupVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		cfg, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		if !verifier.CanVerify(cfg) {
			WriteErrorCode(w, http.StatusUnprocessableEntity, "verify_unsupported",
				"this backup uses a custom command VAC can't verify automatically")
			return
		}
		if latest, err := s.LatestVerification(r.Context(), cfg.ID); err == nil && latest.Status == "running" {
			WriteError(w, http.StatusConflict, "a verification is already running for this backup")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_ = verifier.VerifyOnce(ctx, cfg)
		}()
		audit.SetTarget(r.Context(), "backup", cfg.ID)
		audit.Describe(r.Context(), "verified backup of "+cfg.ServiceName)
		w.WriteHeader(http.StatusAccepted)
	}
}

// ListBackupVerifications returns a config's verification history, newest first.
func ListBackupVerifications(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		cfg, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		rows, err := s.ListVerifications(r.Context(), cid, 50)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list verifications")
			return
		}
		out := make([]verificationDTO, 0, len(rows))
		for _, v := range rows {
			out = append(out, toVerificationDTO(v))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// fleetBackupConfigDTO is a config in the box-wide overview: the per-app shape
// plus its owning app's slug/name so the UI can group and link without a second
// lookup.
type fleetBackupConfigDTO struct {
	backupConfigDTO
	AppSlug string `json:"app_slug"`
	AppName string `json:"app_name"`
}

type uncoveredServiceDTO struct {
	AppID       string `json:"app_id"`
	AppSlug     string `json:"app_slug"`
	AppName     string `json:"app_name"`
	ServiceName string `json:"service_name"`
}

// fleetBackupsDTO is the box-wide Backups overview: a health summary, every
// config (each with its last run), and the volume-bearing services that have no
// backup configured yet.
type fleetBackupsDTO struct {
	Summary struct {
		Configs           int   `json:"configs"`
		FailedLast7d      int   `json:"failed_last_7d"`
		UncoveredServices int   `json:"uncovered_services"`
		LocalBytes        int64 `json:"local_bytes"`
	} `json:"summary"`
	Configs   []fleetBackupConfigDTO `json:"configs"`
	Uncovered []uncoveredServiceDTO  `json:"uncovered"`
}

// ListAllBackups is the box-wide Backups overview (GET /api/backups). It
// aggregates configs across every app, each with its last run, plus a health
// summary and the uncovered-service list. Read-only: config edits stay on the
// per-app surface. local_bytes covers the local destination only.
func ListAllBackups(s *store.Store, workDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		configs, err := s.ListAllBackupConfigs(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list backups")
			return
		}
		uncovered, err := s.ListUncoveredServices(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list services")
			return
		}
		failed, err := s.CountFailedBackupRunsSince(ctx, time.Now().Add(-7*24*time.Hour))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not count backup failures")
			return
		}

		var out fleetBackupsDTO
		out.Configs = make([]fleetBackupConfigDTO, 0, len(configs))
		for _, c := range configs {
			dto := fleetBackupConfigDTO{
				backupConfigDTO: toBackupConfigDTO(c.BackupConfig),
				AppSlug:         c.AppSlug,
				AppName:         c.AppName,
			}
			if last, err := s.LatestBackupRun(ctx, c.ID); err == nil {
				rd := toBackupRunDTO(last)
				dto.LastRun = &rd
			}
			out.Configs = append(out.Configs, dto)
		}

		out.Uncovered = make([]uncoveredServiceDTO, 0, len(uncovered))
		for _, u := range uncovered {
			out.Uncovered = append(out.Uncovered, uncoveredServiceDTO{
				AppID:       u.AppID,
				AppSlug:     u.AppSlug,
				AppName:     u.AppName,
				ServiceName: u.ServiceName,
			})
		}

		// local_bytes is best-effort: a walk failure shouldn't sink the overview,
		// so it falls back to 0 rather than erroring the whole response.
		localBytes, _ := backup.LocalDiskUsage(workDir)

		out.Summary.Configs = len(out.Configs)
		out.Summary.FailedLast7d = failed
		out.Summary.UncoveredServices = len(out.Uncovered)
		out.Summary.LocalBytes = localBytes

		WriteJSON(w, http.StatusOK, out)
	}
}

// CreateBackup adds a per-service backup config. The service must exist on the
// app; S3 credentials are sealed with the master key.
func CreateBackup(s *store.Store, box *crypto.Box) http.HandlerFunc {
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
		var req backupConfigReq
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
		sealed, msg := sealDestConfig(req, box)
		if msg != "" {
			WriteError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		in := toBackupInput(req, sealed)
		c, err := s.CreateBackupConfig(r.Context(), appID, in)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				WriteError(w, http.StatusConflict, "a backup is already configured for this service")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create backup")
			return
		}
		audit.SetTarget(r.Context(), "backup", c.ID)
		audit.Describe(r.Context(), "configured backup for "+app.Slug+"/"+req.ServiceName)
		WriteJSON(w, http.StatusCreated, toBackupConfigDTO(c))
	}
}

// UpdateBackup overwrites a config. When destination is s3 and no fresh
// credentials are sent, the existing sealed blob is preserved.
func UpdateBackup(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		existing, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || existing.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		var req backupConfigReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		req.ServiceName = existing.ServiceName // service is immutable per config
		if msg := req.validate(); msg != "" {
			WriteError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		var sealed []byte
		if req.Destination == "s3" && req.S3 == nil {
			sealed = existing.DestConfig // keep prior creds
		} else {
			var msg string
			if sealed, msg = sealDestConfig(req, box); msg != "" {
				WriteError(w, http.StatusUnprocessableEntity, msg)
				return
			}
		}
		c, err := s.UpdateBackupConfig(r.Context(), appID, cid, toBackupInput(req, sealed))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "backup not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not update backup")
			return
		}
		audit.SetTarget(r.Context(), "backup", c.ID)
		audit.Describe(r.Context(), "updated backup for "+c.ServiceName)
		WriteJSON(w, http.StatusOK, toBackupConfigDTO(c))
	}
}

// DeleteBackup removes a config (its runs cascade).
func DeleteBackup(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		if err := s.DeleteBackupConfig(r.Context(), appID, cid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "backup not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not delete backup")
			return
		}
		audit.SetTarget(r.Context(), "backup", cid)
		audit.Describe(r.Context(), "deleted backup config")
		w.WriteHeader(http.StatusNoContent)
	}
}

// RunBackup triggers a manual backup. Returns 202 — the run executes detached
// off the worker so a long dump doesn't block the request.
func RunBackup(s *store.Store, runner BackupRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		cfg, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_ = runner.RunOnce(ctx, cfg)
		}()
		audit.SetTarget(r.Context(), "backup", cid)
		audit.Describe(r.Context(), "triggered manual backup of "+cfg.ServiceName)
		w.WriteHeader(http.StatusAccepted)
	}
}

// ListBackupRuns returns a config's run history, newest first.
func ListBackupRuns(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		cfg, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		runs, err := s.ListBackupRuns(r.Context(), cid, 50)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list runs")
			return
		}
		out := make([]backupRunDTO, 0, len(runs))
		for _, run := range runs {
			out = append(out, toBackupRunDTO(run))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// DownloadBackup streams a stored artifact (decision #4 — download-only restore).
func DownloadBackup(s *store.Store, box *crypto.Box, workDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rid := chi.URLParam(r, "rid")
		run, err := s.GetBackupRun(r.Context(), rid)
		if err != nil {
			WriteError(w, http.StatusNotFound, "backup run not found")
			return
		}
		if run.Status != "success" || run.ArtifactKey == nil {
			WriteError(w, http.StatusUnprocessableEntity, "this run has no downloadable artifact")
			return
		}
		cfg, err := s.GetBackupConfig(r.Context(), run.ConfigID)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		dest, err := backup.NewDestination(cfg, box, workDir)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not open destination")
			return
		}
		rc, err := dest.Open(r.Context(), *run.ArtifactKey)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not open artifact")
			return
		}
		defer func() { _ = rc.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filenameFromKey(*run.ArtifactKey)+`"`)
		_, _ = io.Copy(w, rc)
	}
}

// RestoreBackup replays a recorded success run back into its target container.
// Destructive — fronted by RequireStepUp (fresh 2FA) at the router and a typed
// confirmation in the UI. Returns 202; the restore runs detached with a 30-min
// timeout like RunBackup. Refuses a custom-command config (422, decision #1) and
// a concurrent restore (409, decision #5) up front.
func RestoreBackup(s *store.Store, restorer BackupRestorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		rid := chi.URLParam(r, "rid")
		run, err := s.GetBackupRun(r.Context(), rid)
		if err != nil {
			WriteError(w, http.StatusNotFound, "backup run not found")
			return
		}
		if run.Status != "success" || run.ArtifactKey == nil {
			WriteError(w, http.StatusUnprocessableEntity, "this run has no restorable artifact")
			return
		}
		cfg, err := s.GetBackupConfig(r.Context(), run.ConfigID)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		if !restorer.CanRestore(cfg) {
			WriteErrorCode(w, http.StatusUnprocessableEntity, "restore_unsupported",
				"this backup uses a custom command VAC can't restore automatically — download the artifact and restore it manually")
			return
		}
		// Reject a second concurrent restore before dispatching (decision #5); the
		// Restorer re-checks under the run row, this just gives a clean 409.
		if latest, err := s.LatestRestoreRun(r.Context(), cfg.ID); err == nil && latest.Status == "running" {
			WriteError(w, http.StatusConflict, "a restore is already running for this backup")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_ = restorer.Restore(ctx, cfg, rid)
		}()
		audit.SetTarget(r.Context(), "backup", cfg.ID)
		audit.Describe(r.Context(), "restored "+cfg.ServiceName+" from backup run "+rid)
		w.WriteHeader(http.StatusAccepted)
	}
}

// ListBackupRestores returns a config's restore history, newest first — backs the
// restore progress/status view.
func ListBackupRestores(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		cid := chi.URLParam(r, "cid")
		cfg, err := s.GetBackupConfig(r.Context(), cid)
		if err != nil || cfg.AppID != appID {
			WriteError(w, http.StatusNotFound, "backup not found")
			return
		}
		restores, err := s.ListRestoreRuns(r.Context(), cid, 50)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list restores")
			return
		}
		out := make([]restoreRunDTO, 0, len(restores))
		for _, rr := range restores {
			out = append(out, toRestoreRunDTO(rr))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// sealDestConfig marshals + seals the destination credentials. Returns ("", "")
// for local; a non-empty message on a sealing problem.
func sealDestConfig(req backupConfigReq, box *crypto.Box) ([]byte, string) {
	if req.Destination != "s3" {
		return nil, ""
	}
	if box == nil {
		return nil, "encryption is disabled (VAC_MASTER_KEY unset); cannot store S3 credentials"
	}
	raw, err := json.Marshal(req.S3)
	if err != nil {
		return nil, "invalid s3 credentials"
	}
	sealed, err := box.Seal(raw)
	if err != nil {
		return nil, "could not encrypt s3 credentials"
	}
	return sealed, ""
}

func toBackupInput(req backupConfigReq, sealed []byte) store.BackupConfigInput {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.BackupConfigInput{
		ServiceName: req.ServiceName,
		Command:     req.Command,
		Frequency:   req.Frequency,
		HourOfDay:   req.HourOfDay,
		DayOfWeek:   req.DayOfWeek,
		Destination: req.Destination,
		DestConfig:  sealed,
		KeepCount:   req.KeepCount,
		Enabled:     enabled,
	}
}

// filenameFromKey returns the last path segment of an artifact key for the
// download filename.
func filenameFromKey(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return key[i+1:]
		}
	}
	return key
}
