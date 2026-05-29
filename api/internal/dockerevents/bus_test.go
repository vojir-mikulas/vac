package dockerevents

import (
	"context"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

type fakeSource struct {
	ch chan dockercli.Event
}

func (f *fakeSource) Events(_ context.Context) (<-chan dockercli.Event, error) {
	return f.ch, nil
}

func TestBusFansOutToAllSubscribers(t *testing.T) {
	src := &fakeSource{ch: make(chan dockercli.Event, 4)}
	bus := NewBus(src, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bus.Run(ctx)

	a, cancelA := bus.Subscribe()
	defer cancelA()
	b, cancelB := bus.Subscribe()
	defer cancelB()

	src.ch <- dockercli.Event{Action: "die"}

	for i, ch := range []<-chan dockercli.Event{a, b} {
		select {
		case ev := <-ch:
			if ev.Action != "die" {
				t.Errorf("sub %d got action %q, want die", i, ev.Action)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timed out", i)
		}
	}
}

func TestBusUnsubscribeStops(t *testing.T) {
	src := &fakeSource{ch: make(chan dockercli.Event, 4)}
	bus := NewBus(src, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bus.Run(ctx)

	ch, unsub := bus.Subscribe()
	unsub()
	unsub() // idempotent — must not panic

	// Channel is closed after unsubscribe.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after unsubscribe")
	}
}
