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

// Worker is the single-goroutine deployment runner. One worker per process
// — concurrent deploys would thrash the build I/O on a typical VPS.
type Worker struct {
	run    RunnerFunc
	sweep  SweeperFunc
	queue  chan string
	wg     sync.WaitGroup
	logger *slog.Logger
}

// NewWorker returns a worker with the given queue capacity. capacity=0
// defaults to 32 — comfortably larger than realistic per-second deploy
// trigger rates.
func NewWorker(run RunnerFunc, sweep SweeperFunc, capacity int, logger *slog.Logger) *Worker {
	if capacity <= 0 {
		capacity = 32
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		run:    run,
		sweep:  sweep,
		queue:  make(chan string, capacity),
		logger: logger,
	}
}

// NewPipelineWorker is the production constructor — wraps a *Pipeline.
func NewPipelineWorker(p *Pipeline, capacity int) *Worker {
	return NewWorker(p.Run, p.Store.MarkInProgressDeploymentsInterrupted, capacity, p.Logger)
}

// Start kicks off the worker goroutine. Returns immediately. The goroutine
// exits when ctx is cancelled — any in-flight deploy gets the cancelled
// context and marks itself `interrupted` on its next status update.
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

	w.wg.Add(1)
	go func() {
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
				start := time.Now()
				if err := w.run(ctx, id); err != nil {
					w.logger.Error("worker: pipeline failed",
						"deployment_id", id, "err", err, "duration", time.Since(start))
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
		return nil
	default:
		return ErrQueueFull
	}
}

// Wait blocks until the worker goroutine returns. Called from main.go
// during graceful shutdown so an in-flight deploy gets time to finish.
func (w *Worker) Wait() { w.wg.Wait() }
