package ws

import (
	"testing"
	"time"
)

func TestHubFanOut(t *testing.T) {
	t.Parallel()
	h := NewHub()
	a, cancelA := h.Subscribe("t")
	defer cancelA()
	b, cancelB := h.Subscribe("t")
	defer cancelB()

	h.Publish("t", []byte("hello"))

	for _, ch := range []<-chan []byte{a, b} {
		select {
		case msg := <-ch:
			if string(msg) != "hello" {
				t.Fatalf("got %q want hello", msg)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for fan-out")
		}
	}
}

func TestHubDropsSlowConsumer(t *testing.T) {
	t.Parallel()
	h := NewHub()
	ch, cancel := h.Subscribe("t")
	defer cancel()

	// Overflow the buffer without draining: the subscriber must be dropped
	// (channel closed) and Publish must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < defaultBuffer+10; i++ {
			h.Publish("t", []byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full slow consumer")
	}

	// Drain until the channel is observed closed.
	closed := false
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-time.After(time.Second):
			t.Fatal("slow consumer channel was never closed")
		}
	}
	if h.HasSubscribers("t") {
		t.Fatal("dropped subscriber should leave the topic empty")
	}
}

func TestHubSubscribeCallbacks(t *testing.T) {
	t.Parallel()
	h := NewHub()
	var subs, unsubs []string
	h.SetCallbacks(
		func(topic string) { subs = append(subs, topic) },
		func(topic string) { unsubs = append(unsubs, topic) },
	)

	_, cancel1 := h.Subscribe("t")
	_, cancel2 := h.Subscribe("t") // second subscriber: no extra onSub
	if len(subs) != 1 {
		t.Fatalf("onSub fired %d times, want 1 (only first subscriber)", len(subs))
	}
	cancel1()
	if len(unsubs) != 0 {
		t.Fatalf("onUnsub fired early: %v", unsubs)
	}
	cancel2() // last subscriber gone
	if len(unsubs) != 1 {
		t.Fatalf("onUnsub fired %d times, want 1 (only last unsubscribe)", len(unsubs))
	}
}

func TestHubCancelIdempotent(t *testing.T) {
	t.Parallel()
	h := NewHub()
	_, cancel := h.Subscribe("t")
	cancel()
	cancel() // must not panic (double close guard)
	if h.HasSubscribers("t") {
		t.Fatal("topic should be empty after cancel")
	}
}

func TestHubCloseDropsSubscribers(t *testing.T) {
	t.Parallel()
	h := NewHub()
	ch, _ := h.Subscribe("t")
	h.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not close subscriber channel")
	}
	// Subscribing after Close returns an already-closed channel.
	ch2, cancel := h.Subscribe("t")
	cancel()
	if _, ok := <-ch2; ok {
		t.Fatal("post-Close Subscribe should return a closed channel")
	}
}
