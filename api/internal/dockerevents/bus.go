// Package dockerevents fans out the host daemon's container event stream to
// multiple in-process consumers. The crash-loop monitor and the runtime-log
// supervisor both need `docker events`; rather than each opening its own
// long-running stream, they subscribe to one Bus. The Bus owns reconnection —
// subscribers keep a stable channel across upstream restarts.
package dockerevents

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// Source provides the raw event stream. *dockercli.Compose satisfies it.
type Source interface {
	Events(ctx context.Context) (<-chan dockercli.Event, error)
}

// subBuffer bounds each subscriber's queue. Consumers (monitor handle, the
// supervisor's debounced enqueue) are fast, so this is generous headroom; a
// full buffer drops the event rather than stalling the whole bus.
const subBuffer = 256

// Bus is a fan-out over one upstream event stream.
type Bus struct {
	src    Source
	logger *slog.Logger

	mu   sync.Mutex
	subs map[chan dockercli.Event]struct{}
}

// NewBus wires a bus over src.
func NewBus(src Source, logger *slog.Logger) *Bus {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bus{src: src, logger: logger, subs: make(map[chan dockercli.Event]struct{})}
}

// Subscribe returns a receive channel and a cancel func. The channel delivers
// every event the bus sees after subscription; it is closed on cancel. cancel
// is idempotent.
func (b *Bus) Subscribe() (<-chan dockercli.Event, func()) {
	ch := make(chan dockercli.Event, subBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
}

func (b *Bus) fanout(ev dockercli.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.logger.Warn("dockerevents: subscriber buffer full, dropping event", "action", ev.Action)
		}
	}
}

// Run consumes the upstream stream and fans events out until ctx is cancelled.
// On a stream error it retries with backoff so a daemon restart doesn't
// permanently disable event-driven monitoring.
func (b *Bus) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if err := b.runOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			b.logger.Warn("dockerevents: stream error; retrying", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (b *Bus) runOnce(ctx context.Context) error {
	ch, err := b.src.Events(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			b.fanout(ev)
		}
	}
}
