package deploy_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
)

func TestWorker_EnqueueRunsRunner(t *testing.T) {
	var got atomic.Int64
	w := deploy.NewWorker(
		func(_ context.Context, id string) error {
			got.Add(1)
			_ = id
			return nil
		},
		nil,
		8,
		1,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	for i := 0; i < 3; i++ {
		if err := w.Enqueue("deploy-" + string(rune('a'+i))); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for got.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() != 3 {
		t.Errorf("runner invocations = %d, want 3", got.Load())
	}
}

func TestWorker_QueueFullReturnsErr(t *testing.T) {
	// Capacity 1, the in-flight runner blocks until released so the queue
	// stays full and the third Enqueue sees ErrQueueFull.
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	var once sync.Once
	w := deploy.NewWorker(
		func(_ context.Context, _ string) error {
			once.Do(func() { close(firstStarted) })
			<-release
			return nil
		},
		nil,
		1,
		1,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if err := w.Enqueue("first"); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	<-firstStarted // worker is now blocked inside the runner
	if err := w.Enqueue("second"); err != nil {
		t.Fatalf("second Enqueue (queued): %v", err)
	}
	if err := w.Enqueue("third"); !errors.Is(err, deploy.ErrQueueFull) {
		t.Errorf("third Enqueue err = %v, want ErrQueueFull", err)
	}
	close(release)
}

func TestWorker_BootSweepRuns(t *testing.T) {
	var swept atomic.Int64
	w := deploy.NewWorker(
		func(_ context.Context, _ string) error { return nil },
		func(_ context.Context) (int64, error) {
			swept.Add(1)
			return 3, nil
		},
		4,
		1,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Sweep is called synchronously during Start; one invocation is enough.
	if swept.Load() != 1 {
		t.Errorf("sweep invocations = %d, want 1", swept.Load())
	}
}

func TestWorker_ConcurrencyRunsInParallel(t *testing.T) {
	// With concurrency 3 and three blocking runners, all three should be in
	// flight at once. A serial worker would only ever have one running.
	const n = 3
	started := make(chan struct{}, n)
	release := make(chan struct{})
	w := deploy.NewWorker(
		func(_ context.Context, _ string) error {
			started <- struct{}{}
			<-release
			return nil
		},
		nil,
		16,
		n,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	for i := 0; i < n; i++ {
		if err := w.Enqueue("d-" + string(rune('a'+i))); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	deadline := time.After(2 * time.Second)
	for got := 0; got < n; got++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("only %d/%d runners started concurrently", got, n)
		}
	}
	close(release)
}

func TestWorker_CancelInterruptsInflight(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	w := deploy.NewWorker(
		func(ctx context.Context, _ string) error {
			close(started)
			<-ctx.Done() // block until the deploy context is cancelled
			close(canceled)
			return ctx.Err()
		},
		nil,
		4,
		1,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	if err := w.Enqueue("victim"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner never started")
	}

	if !w.Cancel("victim") {
		t.Fatal("Cancel returned false for an in-flight deploy")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("runner was not interrupted by Cancel")
	}

	// Cancel of an unknown / already-finished deploy reports false.
	if w.Cancel("ghost") {
		t.Error("Cancel returned true for an unknown deploy")
	}
}

func TestWorker_StopOnContextCancel(t *testing.T) {
	w := deploy.NewWorker(
		func(_ context.Context, _ string) error { return nil },
		nil,
		4,
		1,
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	cancel()
	done := make(chan struct{})
	go func() { w.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop within 1s after ctx cancel")
	}
}
