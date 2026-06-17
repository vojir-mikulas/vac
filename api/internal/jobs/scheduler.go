package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// JobLister loads the scheduler's working set (every enabled job).
type JobLister interface {
	ListEnabledScheduledJobs(ctx context.Context) ([]store.ScheduledJob, error)
}

// Runner executes a single job — satisfied by *Engine.
type Runner interface {
	RunOnce(ctx context.Context, job store.ScheduledJob) error
}

// Scheduler is the backup-pattern goroutine: load enabled jobs, compute the
// soonest next-due time, sleep to it, run everything now due, repeat. One
// sleeping goroutine is the entire idle cost; main.go only starts it when at
// least one enabled job exists.
//
// Two things it does that the backup scheduler doesn't:
//   - Overlap guard: a job already running is skipped, not stacked (decision #4)
//     — tracked by an in-flight set, and excluded from due-time computation so a
//     slow job can't busy-spin the loop.
//   - Completion wake: each run signals the loop when it finishes, so a fresh
//     interval slot is picked up immediately rather than waiting out the idle
//     re-check.
type Scheduler struct {
	store  JobLister
	engine Runner
	logger *slog.Logger
	now    func() time.Time
	// idle is how long to wait before re-checking when nothing is due (so a
	// newly-added job is picked up without a restart). Shorter than the backup
	// scheduler's hour so a fresh job runs soon without needing "run now".
	idle time.Duration

	mu       sync.Mutex
	inFlight map[string]struct{}
	wake     chan struct{}
}

// NewScheduler wires the scheduler.
func NewScheduler(s JobLister, engine Runner, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:    s,
		engine:   engine,
		logger:   logger,
		now:      time.Now,
		idle:     5 * time.Minute,
		inFlight: make(map[string]struct{}),
		wake:     make(chan struct{}, 1),
	}
}

// Run blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		now := s.now()
		jobs, err := s.store.ListEnabledScheduledJobs(ctx)
		if err != nil {
			s.logger.Warn("jobs: load configs", "err", err)
		}

		// Snapshot each job's next-due time before sleeping; after waking, run the
		// ones whose due time has passed. A job already in flight is skipped here
		// entirely — it can't run again until it finishes, so its due time would
		// otherwise pin the loop at wait=0.
		type scheduled struct {
			job store.ScheduledJob
			at  time.Time
		}
		items := make([]scheduled, 0, len(jobs))
		var soonest time.Time
		for _, j := range jobs {
			if s.running(j.ID) {
				continue
			}
			at := nextOccurrence(now, j)
			items = append(items, scheduled{job: j, at: at})
			if soonest.IsZero() || at.Before(soonest) {
				soonest = at
			}
		}

		wait := s.idle
		if !soonest.IsZero() {
			if d := soonest.Sub(now); d > 0 {
				wait = d
			} else {
				wait = 0
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.wake:
		}

		after := s.now()
		for _, it := range items {
			if it.at.After(after) {
				continue
			}
			s.dispatch(ctx, it.job)
		}
	}
}

// dispatch starts a job run in its own goroutine, guarded by the in-flight set
// so a still-running job is skipped rather than stacked. On completion it frees
// the slot and wakes the loop so a fresh interval slot is picked up promptly.
func (s *Scheduler) dispatch(ctx context.Context, job store.ScheduledJob) {
	if !s.markRunning(job.ID) {
		s.logger.Info("jobs: skipped — previous run still in flight", "job", job.ID, "name", job.Name)
		return
	}
	go func() {
		defer func() {
			s.clearRunning(job.ID)
			s.signalWake()
		}()
		if err := s.engine.RunOnce(ctx, job); err != nil {
			// RunOnce already records + notifies; log for the operator trail.
			s.logger.Warn("jobs: scheduled run failed", "job", job.ID, "err", err)
		}
	}()
}

func (s *Scheduler) running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inFlight[id]
	return ok
}

// markRunning adds id to the in-flight set, returning false if it was already
// there (so the caller skips the run).
func (s *Scheduler) markRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inFlight[id]; ok {
		return false
	}
	s.inFlight[id] = struct{}{}
	return true
}

func (s *Scheduler) clearRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}

// signalWake nudges the loop to recompute. Non-blocking: a wake already pending
// is enough.
func (s *Scheduler) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// nextOccurrence computes the next scheduled time at or after now for a job.
//   - interval → anchored on the last run (or now for a never-run job) plus the
//     interval, advanced to the first slot after now so missed slots aren't
//     backfilled and the cadence doesn't drift by run duration.
//   - daily → today (or tomorrow) at hour.
//   - weekly → the next dayOfWeek at hour.
//
// hour is clamped to 0-23; an out-of-range or nil day_of_week for weekly falls
// back to daily semantics. Uses now.Location() (host TZ), like the backup
// scheduler.
func nextOccurrence(now time.Time, job store.ScheduledJob) time.Time {
	if job.Frequency == "interval" {
		step := time.Duration(0)
		if job.IntervalMinutes != nil {
			step = time.Duration(*job.IntervalMinutes) * time.Minute
		}
		if step <= 0 {
			step = time.Hour // defensive: a misconfigured interval falls back to hourly
		}
		anchor := now
		if job.LastRun != nil {
			anchor = *job.LastRun
		}
		cand := anchor.Add(step)
		if !cand.After(now) {
			// Jump straight to the first future slot rather than looping per-step.
			missed := now.Sub(cand)/step + 1
			cand = cand.Add(missed * step)
		}
		return cand
	}

	hour := job.HourOfDay
	if hour < 0 || hour > 23 {
		hour = 3
	}
	base := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())

	if job.Frequency == "weekly" && job.DayOfWeek != nil {
		target := ((*job.DayOfWeek % 7) + 7) % 7
		daysAhead := (target - int(now.Weekday()) + 7) % 7
		cand := base.AddDate(0, 0, daysAhead)
		if !cand.After(now) {
			cand = cand.AddDate(0, 0, 7)
		}
		return cand
	}

	if !base.After(now) {
		base = base.AddDate(0, 0, 1)
	}
	return base
}
