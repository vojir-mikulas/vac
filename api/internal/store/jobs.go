package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ScheduledJob is one user-defined cron entry: a command run on a schedule
// inside one of an app's running service containers (plan: scheduled-jobs.md).
// It mirrors BackupConfig — same scheduler/run lifecycle — minus the
// destination. last_run/next_run are denormalized for the UI; the scheduler
// keeps them current as it runs.
type ScheduledJob struct {
	ID              string
	AppID           string
	Name            string
	ServiceName     string
	Command         string
	Frequency       string // interval | daily | weekly
	IntervalMinutes *int   // when Frequency == "interval"
	HourOfDay       int
	DayOfWeek       *int // 0-6 (Sun=0); NULL unless weekly
	TimeoutSeconds  int
	Enabled         bool
	LastRun         *time.Time
	NextRun         *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ScheduledJobInput is the write shape for create/update.
type ScheduledJobInput struct {
	Name            string
	ServiceName     string
	Command         string
	Frequency       string
	IntervalMinutes *int
	HourOfDay       int
	DayOfWeek       *int
	TimeoutSeconds  int
	Enabled         bool
}

// JobRun is one execution of a ScheduledJob.
type JobRun struct {
	ID         string
	JobID      string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string // running | success | failed | skipped | timeout
	ExitCode   *int
	Output     *string
	Error      *string
}

const scheduledJobColumns = `id, app_id, name, service_name, command, frequency,
	interval_minutes, hour_of_day, day_of_week, timeout_seconds, enabled,
	last_run, next_run, created_at, updated_at`

func scanScheduledJob(row pgx.Row) (ScheduledJob, error) {
	var j ScheduledJob
	err := row.Scan(
		&j.ID, &j.AppID, &j.Name, &j.ServiceName, &j.Command, &j.Frequency,
		&j.IntervalMinutes, &j.HourOfDay, &j.DayOfWeek, &j.TimeoutSeconds, &j.Enabled,
		&j.LastRun, &j.NextRun, &j.CreatedAt, &j.UpdatedAt,
	)
	return j, err
}

// CreateScheduledJob inserts a new job. A second job with the same (app, name)
// collides on the UNIQUE constraint → ErrConflict.
func (s *Store) CreateScheduledJob(ctx context.Context, appID string, in ScheduledJobInput) (ScheduledJob, error) {
	j, err := scanScheduledJob(s.pool.QueryRow(ctx, `
		INSERT INTO scheduled_jobs
			(app_id, name, service_name, command, frequency, interval_minutes, hour_of_day, day_of_week, timeout_seconds, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+scheduledJobColumns,
		appID, in.Name, in.ServiceName, in.Command, in.Frequency, in.IntervalMinutes,
		in.HourOfDay, in.DayOfWeek, in.TimeoutSeconds, in.Enabled))
	if isUniqueViolation(err) {
		return ScheduledJob{}, ErrConflict
	}
	return j, err
}

// UpdateScheduledJob overwrites the mutable fields of an existing job.
func (s *Store) UpdateScheduledJob(ctx context.Context, appID, jobID string, in ScheduledJobInput) (ScheduledJob, error) {
	j, err := scanScheduledJob(s.pool.QueryRow(ctx, `
		UPDATE scheduled_jobs SET
			name             = $3,
			service_name     = $4,
			command          = $5,
			frequency        = $6,
			interval_minutes = $7,
			hour_of_day      = $8,
			day_of_week      = $9,
			timeout_seconds  = $10,
			enabled          = $11,
			updated_at       = NOW()
		WHERE id = $1 AND app_id = $2
		RETURNING `+scheduledJobColumns,
		jobID, appID, in.Name, in.ServiceName, in.Command, in.Frequency, in.IntervalMinutes,
		in.HourOfDay, in.DayOfWeek, in.TimeoutSeconds, in.Enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return ScheduledJob{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return ScheduledJob{}, ErrConflict
	}
	return j, err
}

func (s *Store) GetScheduledJob(ctx context.Context, jobID string) (ScheduledJob, error) {
	j, err := scanScheduledJob(s.pool.QueryRow(ctx, `
		SELECT `+scheduledJobColumns+` FROM scheduled_jobs WHERE id = $1
	`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ScheduledJob{}, ErrNotFound
	}
	return j, err
}

func (s *Store) ListScheduledJobsForApp(ctx context.Context, appID string) ([]ScheduledJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+scheduledJobColumns+` FROM scheduled_jobs WHERE app_id = $1 ORDER BY name
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledJob
	for rows.Next() {
		j, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListEnabledScheduledJobs returns every enabled job across all apps — the
// scheduler's working set for computing the next due time.
func (s *Store) ListEnabledScheduledJobs(ctx context.Context) ([]ScheduledJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+scheduledJobColumns+` FROM scheduled_jobs WHERE enabled = TRUE ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledJob
	for rows.Next() {
		j, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountScheduledJobs reports how many enabled jobs exist — main.go uses it to
// decide whether to start the scheduler goroutine at boot (zero footprint when
// none).
func (s *Store) CountScheduledJobs(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_jobs WHERE enabled = TRUE`).Scan(&n)
	return n, err
}

func (s *Store) DeleteScheduledJob(ctx context.Context, appID, jobID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM scheduled_jobs WHERE id = $1 AND app_id = $2`, jobID, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateJobSchedule records the denormalized last_run/next_run after a run so
// the UI can show "last ran … / next at …" without recomputing the schedule.
func (s *Store) UpdateJobSchedule(ctx context.Context, jobID string, lastRun, nextRun time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE scheduled_jobs SET last_run = $2, next_run = $3 WHERE id = $1
	`, jobID, lastRun, nextRun)
	return err
}

// CreateJobRun opens a run row in the `running` state; FinishJobRun closes it.
func (s *Store) CreateJobRun(ctx context.Context, jobID string) (JobRun, error) {
	var r JobRun
	err := s.pool.QueryRow(ctx, `
		INSERT INTO job_runs (job_id, status) VALUES ($1, 'running')
		RETURNING id, job_id, started_at, finished_at, status, exit_code, output, error
	`, jobID).Scan(&r.ID, &r.JobID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ExitCode, &r.Output, &r.Error)
	return r, err
}

// FinishJobRun records the terminal state. exitCode/output/errMsg are nil where
// not applicable (e.g. a "no running container" failure has none of them).
func (s *Store) FinishJobRun(ctx context.Context, runID, status string, exitCode *int, output, errMsg *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE job_runs
		SET status = $2, exit_code = $3, output = $4, error = $5, finished_at = NOW()
		WHERE id = $1
	`, runID, status, exitCode, output, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteJobRunsOlderThan prunes user-cron run history beyond the retention
// window. Without it a frequently-running job grows job_runs unbounded (a
// per-minute job is ~8 GB/year at the 16 KB output cap); the nightly retention
// pass calls this. Returns the number of rows deleted.
func (s *Store) DeleteJobRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM job_runs WHERE started_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListJobRuns returns the most recent runs for a job, newest first.
func (s *Store) ListJobRuns(ctx context.Context, jobID string, limit int) ([]JobRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, job_id, started_at, finished_at, status, exit_code, output, error
		FROM job_runs WHERE job_id = $1 ORDER BY started_at DESC LIMIT $2
	`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRun
	for rows.Next() {
		var r JobRun
		if err := rows.Scan(&r.ID, &r.JobID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ExitCode, &r.Output, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestJobRun returns the newest run for a job, or ErrNotFound if it has never
// run — backs the per-job status pill in the list view.
func (s *Store) LatestJobRun(ctx context.Context, jobID string) (JobRun, error) {
	var r JobRun
	err := s.pool.QueryRow(ctx, `
		SELECT id, job_id, started_at, finished_at, status, exit_code, output, error
		FROM job_runs WHERE job_id = $1 ORDER BY started_at DESC LIMIT 1
	`, jobID).Scan(&r.ID, &r.JobID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ExitCode, &r.Output, &r.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRun{}, ErrNotFound
	}
	return r, err
}

// CountFailedJobRunsSince counts job runs that ended in failure (failed or
// timeout) on or after `since` — backs an optional sidebar attention badge,
// mirroring CountFailedBackupRunsSince.
func (s *Store) CountFailedJobRunsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM job_runs
		WHERE status IN ('failed', 'timeout') AND finished_at >= $1
	`, since).Scan(&n)
	return n, err
}
