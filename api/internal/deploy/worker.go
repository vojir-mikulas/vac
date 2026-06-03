package deploy

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrQueueFull is returned by Enqueue when the channel is at capacity.
// The handler maps this to HTTP 503.
var ErrQueueFull = errors.New("deploy: queue full")

// RunnerFunc is the unit of work the worker executes. Pipeline.Run
// satisfies this signature.
type RunnerFunc func(ctx context.Context, deploymentID string) error

// SweeperFunc runs once at Worker.Start to mark deployments stuck in a
// non-terminal state from a prior process as `interrupted`.
// store.MarkInProgressDeploymentsInterrupted satisfies this signature.
type SweeperFunc func(ctx context.Context) (int64, error)

// ReaperFunc runs periodically to settle deployments that have hung in a
// non-terminal state for too long. store.ReapStuckDeployments (bound to a
// timeout) satisfies this signature.
type ReaperFunc func(ctx context.Context) (int64, error)

// Reaper defaults. The timeout is deliberately generous: it must exceed the
// slowest realistic build so it never reaps a deploy that's still working.
const (
	defaultReapInterval = 1 * time.Minute
	defaultReapTimeout  = 30 * time.Minute
)

// MaxConcurrency caps the worker pool regardless of the stored setting — past
// this a single VPS thrashes build I/O. Mirrors the DB CHECK in migration 00062.
const MaxConcurrency = 8

// Worker is the deployment runner: a fixed pool of goroutines draining one
// queue. concurrency controls how many deploys run at once across DIFFERENT
// apps — the per-app uniqueness guard (migration 00062) guarantees two workers
// can never pick up two deploys for the same app, so no in-process per-app lock
// is needed.
type Worker struct {
	run          RunnerFunc
	sweep        SweeperFunc
	reap         ReaperFunc
	reapInterval time.Duration
	concurrency  int
	queue        chan string
	wg           sync.WaitGroup
	logger       *slog.Logger

	// pub is the live deploy-queue notifier (nil disables it). A change frame is
	// published whenever a deployment is enqueued or a worker finishes one, so
	// the queue-panel WS refreshes promptly.
	pub Publisher

	// inflight maps a running deployment's id to its cancel func, so a handler
	// can interrupt a specific in-flight deploy without touching the others.
	mu       sync.Mutex
	inflight map[string]context.CancelFunc
}

// NewWorker returns a worker with the given queue capacity and pool size.
// capacity=0 defaults to 32 — comfortably larger than realistic per-second
// deploy trigger rates. concurrency is clamped to 1..MaxConcurrency.
func NewWorker(run RunnerFunc, sweep SweeperFunc, capacity, concurrency int, logger *slog.Logger) *Worker {
	if capacity <= 0 {
		capacity = 32
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		run:         run,
		sweep:       sweep,
		concurrency: clampConcurrency(concurrency),
		queue:       make(chan string, capacity),
		logger:      logger,
		inflight:    make(map[string]context.CancelFunc),
	}
}

// clampConcurrency keeps the pool size in the supported range. A zero or
// negative value (unset setting) falls back to the serial default of 1.
func clampConcurrency(n int) int {
	if n < 1 {
		return 1
	}
	if n > MaxConcurrency {
		return MaxConcurrency
	}
	return n
}

// NewPipelineWorker is the production constructor — wraps a *Pipeline and
// enables the periodic stuck-deployment reaper. concurrency is the deploy-pool
// size (clamped to 1..8).
func NewPipelineWorker(p *Pipeline, capacity, concurrency int) *Worker {
	w := NewWorker(p.Run, p.Store.MarkInProgressDeploymentsInterrupted, capacity, concurrency, p.Logger)
	w.pub = p.Hub
	w.reap = func(ctx context.Context) (int64, error) {
		return p.Store.ReapStuckDeployments(ctx, defaultReapTimeout)
	}
	w.reapInterval = defaultReapInterval
	return w
}

// Start kicks off the worker pool. Returns immediately. The goroutines exit
// when ctx is cancelled — any in-flight deploy gets the cancelled context and
// marks itself `interrupted` on its next status update.
//
// On startup Start sweeps any deployments left non-terminal by a prior
// process — this is the graceful-interrupt mechanism from mvp.md.
func (w *Worker) Start(ctx context.Context) {
	if w.sweep != nil {
		n, err := w.sweep(ctx)
		if err != nil {
			w.logger.Warn("worker: boot sweep failed", "err", err)
		} else if n > 0 {
			w.logger.Info("worker: swept interrupted deployments", "count", n)
		}
	}

	if w.reap != nil && w.reapInterval > 0 {
		w.startReaper(ctx)
	}

	w.logger.Info("worker: starting deploy pool", "concurrency", w.concurrency)
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.loop(ctx)
	}
}

// loop is one pool goroutine: it drains the queue, running each deployment
// under its own cancellable context registered in inflight.
func (w *Worker) loop(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker: shutting down")
			return
		case id, ok := <-w.queue:
			if !ok {
				return
			}
			w.process(ctx, id)
		}
	}
}

// process runs one deployment under a per-deploy cancellable context, so a
// targeted cancel interrupts only this deploy. It always deregisters the cancel
// func and notifies the queue topic on exit.
func (w *Worker) process(ctx context.Context, id string) {
	deployCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.inflight[id] = cancel
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		delete(w.inflight, id)
		w.mu.Unlock()
		cancel()
		PublishDeploymentsChanged(w.pub)
	}()

	start := time.Now()
	if err := w.run(deployCtx, id); err != nil {
		// A cancelled deploy context (targeted cancel or graceful shutdown) is an
		// expected stop, not a pipeline fault — log it calmly.
		if deployCtx.Err() != nil {
			w.logger.Info("worker: deploy interrupted",
				"deployment_id", id, "duration", time.Since(start))
		} else {
			w.logger.Error("worker: pipeline failed",
				"deployment_id", id, "err", err, "duration", time.Since(start))
		}
	}
}

// startReaper runs the stuck-deployment reaper on a ticker until ctx is
// cancelled. It's a best-effort safety net — failures are logged, not fatal.
func (w *Worker) startReaper(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.reapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := w.reap(ctx)
				if err != nil {
					w.logger.Warn("worker: reap stuck deployments failed", "err", err)
				} else if n > 0 {
					w.logger.Warn("worker: reaped stuck deployments", "count", n)
				}
			}
		}
	}()
}

// Enqueue queues a deployment for the worker. Non-blocking — if the queue
// is at capacity, the handler returns 503 and the user retries.
func (w *Worker) Enqueue(deploymentID string) error {
	select {
	case w.queue <- deploymentID:
		PublishDeploymentsChanged(w.pub)
		return nil
	default:
		return ErrQueueFull
	}
}

// Cancel interrupts an in-flight deployment by cancelling its context, which
// aborts the running git/docker subprocess; the pipeline then settles the row as
// `canceled`. Reports whether the deployment was actually running here — a
// queued-but-not-yet-started deploy isn't in the pool, so the handler settles it
// directly (see CancelDeployment).
func (w *Worker) Cancel(deploymentID string) bool {
	w.mu.Lock()
	cancel, ok := w.inflight[deploymentID]
	w.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// NotifyChanged publishes a deploy-queue change frame. Handlers call it after a
// state change the worker itself didn't drive (e.g. cancelling a still-queued
// deployment) so the live queue panel refreshes.
func (w *Worker) NotifyChanged() { PublishDeploymentsChanged(w.pub) }

// Wait blocks until every pool goroutine returns. Called from main.go during
// graceful shutdown so in-flight deploys get time to finish.
func (w *Worker) Wait() { w.wg.Wait() }
